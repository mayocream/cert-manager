/*
Copyright The cert-manager Authors.

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

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	applyconfigurations "github.com/cert-manager/cert-manager/pkg/client/applyconfigurations"
	clientset "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	acmev1 "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/typed/acme/v1"
	fakeacmev1 "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/typed/acme/v1/fake"
	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/typed/certmanager/v1"
	fakecertmanagerv1 "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/typed/certmanager/v1/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/testing"
)

// NewSimpleClientset returns a clientset that will respond with the provided objects.
// It's backed by a very simple object tracker that processes creates, updates and deletions as-is,
// without applying any field management, validations and/or defaults. It shouldn't be considered a replacement
// for a real clientset and is mostly useful in simple unit tests.
//
// DEPRECATED: NewClientset replaces this with support for field management, which significantly improves
// server side apply testing. NewClientset is only available when apply configurations are generated (e.g.
// via --with-applyconfig).
func NewSimpleClientset(objects ...runtime.Object) *Clientset {
	o := testing.NewObjectTracker(scheme, codecs.UniversalDecoder())
	for _, obj := range objects {
		if err := o.Add(obj); err != nil {
			panic(err)
		}
	}

	cs := &Clientset{tracker: o}
	cs.discovery = &fakediscovery.FakeDiscovery{Fake: &cs.Fake}
	cs.AddReactor("*", "*", testing.ObjectReaction(o))
	cs.AddWatchReactor("*", func(action testing.Action) (handled bool, ret watch.Interface, err error) {
		var opts metav1.ListOptions
		if watchActcion, ok := action.(testing.WatchActionImpl); ok {
			opts = watchActcion.ListOptions
		}
		gvr := action.GetResource()
		ns := action.GetNamespace()
		watch, err := o.Watch(gvr, ns, opts)
		if err != nil {
			return false, nil, err
		}
		return true, watch, nil
	})

	return cs
}

// Clientset implements clientset.Interface. Meant to be embedded into a
// struct to get a default implementation. This makes faking out just the method
// you want to test easier.
type Clientset struct {
	testing.Fake
	discovery *fakediscovery.FakeDiscovery
	tracker   testing.ObjectTracker
}

func (c *Clientset) Discovery() discovery.DiscoveryInterface {
	return c.discovery
}

func (c *Clientset) Tracker() testing.ObjectTracker {
	return c.tracker
}

// NewClientset returns a clientset that will respond with the provided objects.
// It's backed by a very simple object tracker that processes creates, updates and deletions as-is,
// without applying any validations and/or defaults. It shouldn't be considered a replacement
// for a real clientset and is mostly useful in simple unit tests.
func NewClientset(objects ...runtime.Object) *Clientset {
	o := testing.NewFieldManagedObjectTracker(
		scheme,
		codecs.UniversalDecoder(),
		applyconfigurations.NewTypeConverter(scheme),
	)
	for _, obj := range objects {
		if err := o.Add(obj); err != nil {
			panic(err)
		}
	}

	cs := &Clientset{tracker: o}
	cs.discovery = &fakediscovery.FakeDiscovery{Fake: &cs.Fake}
	cs.AddReactor("*", "*", testing.ObjectReaction(o))
	cs.AddWatchReactor("*", func(action testing.Action) (handled bool, ret watch.Interface, err error) {
		var opts metav1.ListOptions
		if watchActcion, ok := action.(testing.WatchActionImpl); ok {
			opts = watchActcion.ListOptions
		}
		gvr := action.GetResource()
		ns := action.GetNamespace()
		watch, err := o.Watch(gvr, ns, opts)
		if err != nil {
			return false, nil, err
		}
		return true, watch, nil
	})

	return cs
}

var (
	_ clientset.Interface = &Clientset{}
	_ testing.FakeClient  = &Clientset{}
)

// AcmeV1 retrieves the AcmeV1Client
func (c *Clientset) AcmeV1() acmev1.AcmeV1Interface {
	return &fakeacmev1.FakeAcmeV1{Fake: &c.Fake}
}

// CertmanagerV1 retrieves the CertmanagerV1Client
func (c *Clientset) CertmanagerV1() certmanagerv1.CertmanagerV1Interface {
	return &fakecertmanagerv1.FakeCertmanagerV1{Fake: &c.Fake}
}
