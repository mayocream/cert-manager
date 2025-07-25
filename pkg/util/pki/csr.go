/*
Copyright 2020 The cert-manager Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pki

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"

	apiutil "github.com/cert-manager/cert-manager/pkg/api/util"
	v1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
)

// IPAddressesToString converts a slice of IP addresses to strings, which can be useful for
// printing a list of addresses but MUST NOT be used for comparing two slices of IP addresses.
func IPAddressesToString(ipAddresses []net.IP) []string {
	var ipNames []string
	for _, ip := range ipAddresses {
		ipNames = append(ipNames, ip.String())
	}
	return ipNames
}

func IPAddressesFromStrings(ipStrings []string) ([]net.IP, error) {
	var ipAddresses []net.IP
	for _, ipString := range ipStrings {
		ip, err := netip.ParseAddr(ipString)
		if err != nil || ip.Zone() != "" {
			return nil, err
		}
		addr := ip.AsSlice()
		if len(addr) == 0 {
			return nil, fmt.Errorf("failed to parse IP address %q", ipString)
		}
		ipAddresses = append(ipAddresses, net.IP(addr))
	}
	return ipAddresses, nil
}

func URLsToString(uris []*url.URL) []string {
	var uriStrs []string
	for _, uri := range uris {
		if uri == nil {
			panic("provided uri to string is nil")
		}

		uriStrs = append(uriStrs, uri.String())
	}

	return uriStrs
}

// SubjectForCertificate will return the Subject from the Certificate resource or an empty one if it is not set
func SubjectForCertificate(crt *v1.Certificate) v1.X509Subject {
	if crt.Spec.Subject == nil {
		return v1.X509Subject{}
	}

	return *crt.Spec.Subject
}

func KeyUsagesForCertificateOrCertificateRequest(usages []v1.KeyUsage, isCA bool) (ku x509.KeyUsage, eku []x509.ExtKeyUsage, err error) {
	var unk []v1.KeyUsage
	if isCA {
		ku |= x509.KeyUsageCertSign
	}

	// If no usages are specified, default to the ones specified in the
	// Kubernetes API.
	if len(usages) == 0 {
		usages = v1.DefaultKeyUsages()
	}

	for _, u := range usages {
		if kuse, ok := apiutil.KeyUsageType(u); ok {
			ku |= kuse
		} else if ekuse, ok := apiutil.ExtKeyUsageType(u); ok {
			eku = append(eku, ekuse)
		} else {
			unk = append(unk, u)
		}
	}
	if len(unk) > 0 {
		err = fmt.Errorf("unknown key usages: %v", unk)
	}
	return
}

type generateCSROptions struct {
	EncodeBasicConstraintsInRequest bool
	EncodeNameConstraints           bool
	EncodeOtherNames                bool
	UseLiteralSubject               bool
}

type GenerateCSROption func(*generateCSROptions)

// WithEncodeBasicConstraintsInRequest determines whether the BasicConstraints
// extension should be encoded in the CSR.
// NOTE: this is a temporary option that will be removed in a future release.
func WithEncodeBasicConstraintsInRequest(encode bool) GenerateCSROption {
	return func(o *generateCSROptions) {
		o.EncodeBasicConstraintsInRequest = encode
	}
}

func WithNameConstraints(enabled bool) GenerateCSROption {
	return func(o *generateCSROptions) {
		o.EncodeNameConstraints = enabled
	}
}

func WithOtherNames(enabled bool) GenerateCSROption {
	return func(o *generateCSROptions) {
		o.EncodeOtherNames = enabled
	}
}

func WithUseLiteralSubject(useLiteralSubject bool) GenerateCSROption {
	return func(o *generateCSROptions) {
		o.UseLiteralSubject = useLiteralSubject
	}
}

// GenerateCSR will generate a new *x509.CertificateRequest template to be used
// by issuers that utilise CSRs to obtain Certificates.
// The CSR will not be signed, and should be passed to either EncodeCSR or
// to the x509.CreateCertificateRequest function.
func GenerateCSR(crt *v1.Certificate, optFuncs ...GenerateCSROption) (*x509.CertificateRequest, error) {
	opts := &generateCSROptions{
		EncodeBasicConstraintsInRequest: false,
		EncodeNameConstraints:           false,
		EncodeOtherNames:                false,
		UseLiteralSubject:               false,
	}
	for _, opt := range optFuncs {
		opt(opts)
	}

	// Generate the Subject field for the CSR.
	var commonName string
	var rdnSubject pkix.RDNSequence
	if opts.UseLiteralSubject && len(crt.Spec.LiteralSubject) > 0 {
		subjectRDNSequence, err := UnmarshalSubjectStringToRDNSequence(crt.Spec.LiteralSubject)
		if err != nil {
			return nil, err
		}

		commonName = ExtractCommonNameFromRDNSequence(subjectRDNSequence)
		rdnSubject = subjectRDNSequence
	} else {
		subject := SubjectForCertificate(crt)

		commonName = crt.Spec.CommonName
		rdnSubject = pkix.Name{
			Country:            subject.Countries,
			Organization:       subject.Organizations,
			OrganizationalUnit: subject.OrganizationalUnits,
			Locality:           subject.Localities,
			Province:           subject.Provinces,
			StreetAddress:      subject.StreetAddresses,
			PostalCode:         subject.PostalCodes,
			SerialNumber:       subject.SerialNumber,
			CommonName:         commonName,
		}.ToRDNSequence()
	}

	// Generate the SANs for the CSR.
	ipAddresses, err := IPAddressesFromStrings(crt.Spec.IPAddresses)
	if err != nil {
		return nil, err
	}

	sans := GeneralNames{
		RFC822Names:                crt.Spec.EmailAddresses,
		DNSNames:                   crt.Spec.DNSNames,
		UniformResourceIdentifiers: crt.Spec.URIs,
		IPAddresses:                ipAddresses,
	}

	if opts.EncodeOtherNames {
		for _, otherName := range crt.Spec.OtherNames {
			oid, err := ParseObjectIdentifier(otherName.OID)
			if err != nil {
				return nil, err
			}

			value, err := MarshalUniversalValue(UniversalValue{
				UTF8String: otherName.UTF8Value,
			})
			if err != nil {
				return nil, err
			}

			sans.OtherNames = append(sans.OtherNames, OtherName{
				TypeID: oid,
				Value: asn1.RawValue{
					Tag:        0,
					Class:      asn1.ClassContextSpecific,
					IsCompound: true,
					Bytes:      value,
				},
			})
		}
	}

	if len(commonName) == 0 && sans.Empty() {
		return nil, fmt.Errorf("no common name (from the commonName field or from a literalSubject), DNS name, URI SAN, Email SAN, IP or OtherName SAN specified on certificate")
	}

	pubKeyAlgo, sigAlgo, err := SignatureAlgorithm(crt)
	if err != nil {
		return nil, err
	}

	asn1Subject, err := MarshalRDNSequenceToRawDERBytes(rdnSubject)
	if err != nil {
		return nil, err
	}

	var extraExtensions []pkix.Extension

	if !sans.Empty() {
		sanExtension, err := MarshalSANs(sans, !IsASN1SubjectEmpty(asn1Subject))
		if err != nil {
			return nil, err
		}
		extraExtensions = append(extraExtensions, sanExtension)
	}

	if crt.Spec.EncodeUsagesInRequest == nil || *crt.Spec.EncodeUsagesInRequest {
		ku, ekus, err := KeyUsagesForCertificateOrCertificateRequest(crt.Spec.Usages, crt.Spec.IsCA)
		if err != nil {
			return nil, fmt.Errorf("failed to build key usages: %w", err)
		}

		if ku != 0 {
			usage, err := MarshalKeyUsage(ku)
			if err != nil {
				return nil, fmt.Errorf("failed to asn1 encode usages: %w", err)
			}
			extraExtensions = append(extraExtensions, usage)
		}

		// Only add extended usages if they are specified.
		if len(ekus) > 0 {
			extendedUsages, err := MarshalExtKeyUsage(ekus, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to asn1 encode extended usages: %w", err)
			}
			extraExtensions = append(extraExtensions, extendedUsages)
		}
	}

	// NOTE(@inteon): opts.EncodeBasicConstraintsInRequest is a temporary solution and will
	// be removed/ replaced in a future release.
	if opts.EncodeBasicConstraintsInRequest {
		basicExtension, err := MarshalBasicConstraints(crt.Spec.IsCA, nil)
		if err != nil {
			return nil, err
		}
		extraExtensions = append(extraExtensions, basicExtension)
	}

	if opts.EncodeNameConstraints && crt.Spec.NameConstraints != nil {
		nameConstraints := &NameConstraints{}

		if crt.Spec.NameConstraints.Permitted != nil {
			nameConstraints.PermittedDNSDomains = crt.Spec.NameConstraints.Permitted.DNSDomains
			nameConstraints.PermittedIPRanges, err = parseCIDRs(crt.Spec.NameConstraints.Permitted.IPRanges)
			if err != nil {
				return nil, err
			}
			nameConstraints.PermittedEmailAddresses = crt.Spec.NameConstraints.Permitted.EmailAddresses
			nameConstraints.PermittedURIDomains = crt.Spec.NameConstraints.Permitted.URIDomains
		}

		if crt.Spec.NameConstraints.Excluded != nil {
			nameConstraints.ExcludedDNSDomains = crt.Spec.NameConstraints.Excluded.DNSDomains
			nameConstraints.ExcludedIPRanges, err = parseCIDRs(crt.Spec.NameConstraints.Excluded.IPRanges)
			if err != nil {
				return nil, err
			}
			nameConstraints.ExcludedEmailAddresses = crt.Spec.NameConstraints.Excluded.EmailAddresses
			nameConstraints.ExcludedURIDomains = crt.Spec.NameConstraints.Excluded.URIDomains
		}

		if !nameConstraints.IsEmpty() {
			extension, err := MarshalNameConstraints(nameConstraints, crt.Spec.NameConstraints.Critical)
			if err != nil {
				return nil, err
			}

			extraExtensions = append(extraExtensions, extension)
		}
	}

	cr := &x509.CertificateRequest{
		// Version 0 is the only one defined in the PKCS#10 standard, RFC2986.
		// This value isn't used by Go at the time of writing.
		// https://datatracker.ietf.org/doc/html/rfc2986#section-4
		Version:            0,
		SignatureAlgorithm: sigAlgo,
		PublicKeyAlgorithm: pubKeyAlgo,
		RawSubject:         asn1Subject,
		ExtraExtensions:    extraExtensions,
	}

	return cr, nil
}

// SignCertificate returns a signed *x509.Certificate given a template
// *x509.Certificate crt and an issuer.
// publicKey is the public key of the signee, and signerKey is the private
// key of the signer.
// It returns a PEM encoded copy of the Certificate as well as a *x509.Certificate
// which can be used for reading the encoded values.
func SignCertificate(template *x509.Certificate, issuerCert *x509.Certificate, publicKey crypto.PublicKey, signerKey any) ([]byte, *x509.Certificate, error) {
	typedSigner, ok := signerKey.(crypto.Signer)
	if !ok {
		return nil, nil, fmt.Errorf("didn't get an expected Signer in call to SignCertificate")
	}

	var pubKeyAlgo x509.PublicKeyAlgorithm
	var sigAlgoArg any

	// NB: can't rely on issuerCert.Public or issuercert.PublicKeyAlgorithm being set reliably;
	// but we know that signerKey.Public() will work!
	switch pubKey := typedSigner.Public().(type) {
	case *rsa.PublicKey:
		pubKeyAlgo = x509.RSA

		// Size is in bytes so multiply by 8 to get bits because they're more familiar
		// This is technically not portable but if you're using cert-manager on a platform
		// with bytes that don't have 8 bits, you've got bigger problems than this!
		sigAlgoArg = pubKey.Size() * 8

	case *ecdsa.PublicKey:
		pubKeyAlgo = x509.ECDSA
		sigAlgoArg = pubKey.Curve

	case ed25519.PublicKey:
		pubKeyAlgo = x509.Ed25519
		sigAlgoArg = nil // ignored by signatureAlgorithmFromPublicKey

	default:
		return nil, nil, fmt.Errorf("unknown public key type on signing certificate: %T", issuerCert.PublicKey)
	}

	var err error
	template.SignatureAlgorithm, err = signatureAlgorithmFromPublicKey(pubKeyAlgo, sigAlgoArg)
	if err != nil {
		return nil, nil, err
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, issuerCert, publicKey, signerKey)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating x509 certificate: %s", err.Error())
	}

	cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("error decoding DER certificate bytes: %s", err.Error())
	}

	pemBytes := bytes.NewBuffer([]byte{})
	err = pem.Encode(pemBytes, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if err != nil {
		return nil, nil, fmt.Errorf("error encoding certificate PEM: %s", err.Error())
	}

	return pemBytes.Bytes(), cert, err
}

// SignCSRTemplate signs a certificate template usually based upon a CSR. This
// function expects all fields to be present in the certificate template,
// including its public key.
// It returns the PEM bundle containing certificate data and the CA data, encoded in PEM format.
func SignCSRTemplate(caCerts []*x509.Certificate, caPrivateKey crypto.Signer, template *x509.Certificate) (PEMBundle, error) {
	if len(caCerts) == 0 {
		return PEMBundle{}, errors.New("no CA certificates given to sign CSR template")
	}

	issuingCACert := caCerts[0]

	_, cert, err := SignCertificate(template, issuingCACert, template.PublicKey, caPrivateKey)
	if err != nil {
		return PEMBundle{}, err
	}

	bundle, err := ParseSingleCertificateChain(append(caCerts, cert))
	if err != nil {
		return PEMBundle{}, err
	}

	return bundle, nil
}

// EncodeCSR calls x509.CreateCertificateRequest to sign the given CSR template.
// It returns a DER encoded signed CSR.
func EncodeCSR(template *x509.CertificateRequest, key crypto.Signer) ([]byte, error) {
	derBytes, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return nil, fmt.Errorf("error creating x509 certificate: %s", err.Error())
	}

	return derBytes, nil
}

// EncodeX509 will encode a single *x509.Certificate into PEM format.
func EncodeX509(cert *x509.Certificate) ([]byte, error) {
	caPem := bytes.NewBuffer([]byte{})
	err := pem.Encode(caPem, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err != nil {
		return nil, err
	}

	return caPem.Bytes(), nil
}

// EncodeX509Chain will encode a list of *x509.Certificates into a PEM format chain.
// Self-signed certificates are not included as per
// https://datatracker.ietf.org/doc/html/rfc5246#section-7.4.2
// Certificates are output in the order they're given; if the input is not ordered
// as specified in RFC5246 section 7.4.2, the resulting chain might not be valid
// for use in TLS.
func EncodeX509Chain(certs []*x509.Certificate) ([]byte, error) {
	caPem := bytes.NewBuffer([]byte{})
	for _, cert := range certs {
		if cert == nil {
			continue
		}

		if cert.CheckSignatureFrom(cert) == nil {
			// Don't include self-signed certificate
			continue
		}

		err := pem.Encode(caPem, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
		if err != nil {
			return nil, err
		}
	}

	return caPem.Bytes(), nil
}

var keyAlgorithms = map[v1.PrivateKeyAlgorithm]x509.PublicKeyAlgorithm{
	v1.RSAKeyAlgorithm:     x509.RSA,
	v1.ECDSAKeyAlgorithm:   x509.ECDSA,
	v1.Ed25519KeyAlgorithm: x509.Ed25519,
}
var sigAlgorithms = map[v1.SignatureAlgorithm]x509.SignatureAlgorithm{
	v1.SHA256WithRSA:   x509.SHA256WithRSA,
	v1.SHA384WithRSA:   x509.SHA384WithRSA,
	v1.SHA512WithRSA:   x509.SHA512WithRSA,
	v1.ECDSAWithSHA256: x509.ECDSAWithSHA256,
	v1.ECDSAWithSHA384: x509.ECDSAWithSHA384,
	v1.ECDSAWithSHA512: x509.ECDSAWithSHA512,
	v1.PureEd25519:     x509.PureEd25519,
}

// SignatureAlgorithm will determine the appropriate signature algorithm for
// the given certificate.
// Adapted from https://github.com/cloudflare/cfssl/blob/master/csr/csr.go#L102
func SignatureAlgorithm(crt *v1.Certificate) (x509.PublicKeyAlgorithm, x509.SignatureAlgorithm, error) {
	var pubKeyAlgo x509.PublicKeyAlgorithm
	var specAlgorithm v1.PrivateKeyAlgorithm
	var specKeySize int

	if crt.Spec.PrivateKey != nil {
		specAlgorithm = crt.Spec.PrivateKey.Algorithm
		specKeySize = crt.Spec.PrivateKey.Size
	}

	var sigAlgoArg any

	var ok bool
	if specAlgorithm == "" {
		pubKeyAlgo = x509.RSA
	} else {
		pubKeyAlgo, ok = keyAlgorithms[specAlgorithm]
		if !ok {
			return x509.UnknownPublicKeyAlgorithm, x509.UnknownSignatureAlgorithm, fmt.Errorf("unsupported algorithm specified: %s. should be either 'ecdsa', 'ed25519' or 'rsa", crt.Spec.PrivateKey.Algorithm)
		}
	}

	var sigAlgo x509.SignatureAlgorithm
	if crt.Spec.SignatureAlgorithm != "" {
		sigAlgo, ok = sigAlgorithms[crt.Spec.SignatureAlgorithm]
		if !ok {
			return x509.UnknownPublicKeyAlgorithm, x509.UnknownSignatureAlgorithm, fmt.Errorf("unsupported signature algorithm: %s", crt.Spec.SignatureAlgorithm)
		}
		return pubKeyAlgo, sigAlgo, nil
	}

	switch pubKeyAlgo {
	case x509.RSA:
		if specKeySize == 0 {
			sigAlgoArg = MinRSAKeySize
		} else {
			sigAlgoArg = specKeySize
		}
	case x509.ECDSA:
		switch specKeySize {
		case 521:
			sigAlgoArg = elliptic.P521()
		case 384:
			sigAlgoArg = elliptic.P384()
		case 256, 0:
			sigAlgoArg = elliptic.P256()
		default:
			return x509.UnknownPublicKeyAlgorithm, x509.UnknownSignatureAlgorithm, fmt.Errorf("unsupported ecdsa keysize specified: %d", crt.Spec.PrivateKey.Size)
		}
	}

	sigAlgo, err := signatureAlgorithmFromPublicKey(pubKeyAlgo, sigAlgoArg)
	if err != nil {
		return x509.UnknownPublicKeyAlgorithm, x509.UnknownSignatureAlgorithm, err
	}

	return pubKeyAlgo, sigAlgo, nil
}

// signatureAlgorithmFromPublicKey takes a public key type and an argument specific to that public
// key, and returns an appropriate signature algorithm for that key.
// If alg is x509.RSA, arg must be an integer key size in bits
// If alg is x509.ECDSA, arg must be an elliptic.Curve
// If alg is x509.Ed25519, arg is ignored
// All other algorithms and args cause an error
// The signature algorithms returned by this function are to some degree a matter of preference. The
// choices here are motivated by what is common and what is required by bodies such as the US DoD.
func signatureAlgorithmFromPublicKey(alg x509.PublicKeyAlgorithm, arg any) (x509.SignatureAlgorithm, error) {
	var signatureAlgorithm x509.SignatureAlgorithm

	switch alg {
	case x509.RSA:
		size, ok := arg.(int)
		if !ok {
			return x509.UnknownSignatureAlgorithm, fmt.Errorf("expected to get an integer key size for RSA key but got %T", arg)
		}

		switch {
		case size >= 4096:
			signatureAlgorithm = x509.SHA512WithRSA

		case size >= 3072:
			signatureAlgorithm = x509.SHA384WithRSA

		case size >= 2048:
			signatureAlgorithm = x509.SHA256WithRSA

		default:
			return x509.UnknownSignatureAlgorithm, fmt.Errorf("invalid size %d for RSA key on signing certificate", size)
		}

	case x509.ECDSA:
		curve, ok := arg.(elliptic.Curve)
		if !ok {
			return x509.UnknownSignatureAlgorithm, fmt.Errorf("expected to get an ECDSA curve for ECDSA key but got %T", arg)
		}

		switch curve {
		case elliptic.P521():
			signatureAlgorithm = x509.ECDSAWithSHA512

		case elliptic.P384():
			signatureAlgorithm = x509.ECDSAWithSHA384

		case elliptic.P256():
			signatureAlgorithm = x509.ECDSAWithSHA256

		default:
			return x509.UnknownSignatureAlgorithm, fmt.Errorf("unknown / unsupported curve attached to ECDSA signing certificate")
		}

	case x509.Ed25519:
		signatureAlgorithm = x509.PureEd25519

	default:
		return x509.UnknownSignatureAlgorithm, fmt.Errorf("got unsupported public key type when trying to calculate signature algorithm")
	}

	return signatureAlgorithm, nil
}
