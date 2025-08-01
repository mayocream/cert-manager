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

package certificate

import (
	"context"
	"crypto/x509"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cert-manager/cert-manager/e2e-tests/framework"
	"github.com/cert-manager/cert-manager/e2e-tests/suite/issuers/acme/dnsproviders"
	"github.com/cert-manager/cert-manager/e2e-tests/util"
	cmacme "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	v1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/cert-manager/cert-manager/test/unit/gen"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const testingACMEEmail = "e2e@cert-manager.io"
const testingACMEPrivateKey = "test-acme-private-key"

var _ = framework.CertManagerDescribe("ACME CertificateRequest (DNS01)", func() {
	// TODO: add better logic to handle other DNS providers
	testRFC2136DNSProvider()
})

func testRFC2136DNSProvider() bool {
	name := "rfc2136"
	return Context("With "+name+" credentials configured", func() {
		f := framework.NewDefaultFramework("create-acme-certificate-request-dns01-" + name)
		h := f.Helper()

		issuerName := "test-acme-issuer"
		certificateRequestName := "test-acme-certificate-request"
		dnsDomain := ""

		p := &dnsproviders.RFC2136{}
		f.RequireAddon(p)

		BeforeEach(func(testingCtx context.Context) {
			By("Creating an Issuer")
			dnsDomain = util.RandomSubdomain(p.Details().BaseDomain)
			issuer := gen.Issuer(issuerName,
				gen.SetIssuerACME(cmacme.ACMEIssuer{
					SkipTLSVerify: true,
					Server:        f.Config.Addons.ACMEServer.URL,
					Email:         testingACMEEmail,
					PrivateKey: cmmeta.SecretKeySelector{
						LocalObjectReference: cmmeta.LocalObjectReference{
							Name: testingACMEPrivateKey,
						},
					},
					Solvers: []cmacme.ACMEChallengeSolver{
						{
							DNS01: &p.Details().ProviderConfig,
						},
					},
				}))
			issuer.Namespace = f.Namespace.Name
			_, err := f.CertManagerClientSet.CertmanagerV1().Issuers(f.Namespace.Name).Create(testingCtx, issuer, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			By("Waiting for Issuer to become Ready")
			err = util.WaitForIssuerCondition(testingCtx, f.CertManagerClientSet.CertmanagerV1().Issuers(f.Namespace.Name),
				issuerName,
				v1.IssuerCondition{
					Type:   v1.IssuerConditionReady,
					Status: cmmeta.ConditionTrue,
				})
			Expect(err).NotTo(HaveOccurred())
			By("Verifying the ACME account URI is set")
			err = util.WaitForIssuerStatusFunc(testingCtx, f.CertManagerClientSet.CertmanagerV1().Issuers(f.Namespace.Name),
				issuerName,
				func(i *v1.Issuer) (bool, error) {
					if i.GetStatus().ACMEStatus().URI == "" {
						return false, nil
					}
					return true, nil
				})
			Expect(err).NotTo(HaveOccurred())
			By("Verifying ACME account private key exists")
			secret, err := f.KubeClientSet.CoreV1().Secrets(f.Namespace.Name).Get(testingCtx, testingACMEPrivateKey, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			if len(secret.Data) != 1 {
				Fail("Expected 1 key in ACME account private key secret, but there was %d", len(secret.Data))
			}
		})

		AfterEach(func(testingCtx context.Context) {
			By("Cleaning up")
			err := f.CertManagerClientSet.CertmanagerV1().Issuers(f.Namespace.Name).Delete(testingCtx, issuerName, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
			err = f.KubeClientSet.CoreV1().Secrets(f.Namespace.Name).Delete(testingCtx, testingACMEPrivateKey, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should obtain a signed certificate for a regular domain", func(testingCtx context.Context) {
			By("Creating a CertificateRequest")

			crClient := f.CertManagerClientSet.CertmanagerV1().CertificateRequests(f.Namespace.Name)

			csr, key, err := gen.CSR(x509.RSA, gen.SetCSRCommonName(dnsDomain), gen.SetCSRDNSNames(dnsDomain))
			Expect(err).NotTo(HaveOccurred())
			cr := gen.CertificateRequest(certificateRequestName,
				gen.SetCertificateRequestNamespace(f.Namespace.Name),
				gen.SetCertificateRequestIssuer(cmmeta.ObjectReference{Kind: v1.IssuerKind, Name: issuerName}),
				gen.SetCertificateRequestCSR(csr),
			)

			_, err = crClient.Create(testingCtx, cr, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			err = h.WaitCertificateRequestIssuedValid(testingCtx, f.Namespace.Name, certificateRequestName, time.Minute*5, key)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should obtain a signed certificate for a wildcard domain", func(testingCtx context.Context) {
			By("Creating a CertificateRequest")

			csr, key, err := gen.CSR(x509.RSA, gen.SetCSRCommonName("*."+dnsDomain), gen.SetCSRDNSNames("*."+dnsDomain))
			Expect(err).NotTo(HaveOccurred())
			cr := gen.CertificateRequest(certificateRequestName,
				gen.SetCertificateRequestNamespace(f.Namespace.Name),
				gen.SetCertificateRequestIssuer(cmmeta.ObjectReference{Kind: v1.IssuerKind, Name: issuerName}),
				gen.SetCertificateRequestCSR(csr),
			)

			_, err = f.CertManagerClientSet.CertmanagerV1().CertificateRequests(f.Namespace.Name).Create(testingCtx, cr, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			err = h.WaitCertificateRequestIssuedValid(testingCtx, f.Namespace.Name, certificateRequestName, time.Minute*5, key)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should obtain a signed certificate for a wildcard and apex domain", func(testingCtx context.Context) {
			By("Creating a CertificateRequest")

			csr, key, err := gen.CSR(x509.RSA, gen.SetCSRCommonName("*."+dnsDomain), gen.SetCSRDNSNames("*."+dnsDomain, dnsDomain))
			Expect(err).NotTo(HaveOccurred())
			cr := gen.CertificateRequest(certificateRequestName,
				gen.SetCertificateRequestNamespace(f.Namespace.Name),
				gen.SetCertificateRequestIssuer(cmmeta.ObjectReference{Kind: v1.IssuerKind, Name: issuerName}),
				gen.SetCertificateRequestCSR(csr),
			)

			_, err = f.CertManagerClientSet.CertmanagerV1().CertificateRequests(f.Namespace.Name).Create(testingCtx, cr, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			// use a longer timeout for this, as it requires performing 2 dns validations in serial
			err = h.WaitCertificateRequestIssuedValid(testingCtx, f.Namespace.Name, certificateRequestName, time.Minute*10, key)
			Expect(err).NotTo(HaveOccurred())
		})
	})
}
