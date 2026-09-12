package certs

import "testing"

// A kept certificate is only any use for the names it was issued for. The panel
// answering on a name it has never heard of is not hypothetical: a master
// reached at a second DNS name turns away every node that verifies properly,
// which is every node.
func TestStoredCertificateIsReissuedForNewNames(t *testing.T) {
	store := newMemStore()

	first, _, src, err := Ensure(Options{Store: store, Hosts: []string{"10.0.0.1", "panel-a.example.com"}})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if src != Generated {
		t.Fatalf("source = %v, want Generated", src)
	}
	if !Covers(first, []string{"panel-a.example.com"}) {
		t.Fatal("the certificate does not cover the name it was issued for")
	}
	if Covers(first, []string{"panel-b.example.com"}) {
		t.Fatal("the certificate covers a name it was never given")
	}

	// Asked for again with the same names, the kept one is served.
	again, _, src, err := Ensure(Options{Store: store, Hosts: []string{"10.0.0.1", "panel-a.example.com"}})
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if src != FromStore {
		t.Errorf("source = %v, want FromStore: an unchanged certificate must be kept", src)
	}
	if string(again) != string(first) {
		t.Error("a certificate that still covers every name was replaced anyway")
	}

	// A name it cannot serve means a new one.
	wider := []string{"10.0.0.1", "panel-a.example.com", "panel-b.example.com:443"}
	third, _, src, err := Ensure(Options{Store: store, Hosts: wider})
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if src != Generated {
		t.Fatalf("source = %v, want Generated: the panel now answers on a name the "+
			"certificate never carried", src)
	}
	for _, h := range []string{"panel-a.example.com", "panel-b.example.com", "10.0.0.1"} {
		if !Covers(third, []string{h}) {
			t.Errorf("the reissued certificate does not cover %q", h)
		}
	}

	// And the new one is what is kept from then on.
	kept, _, src, err := Ensure(Options{Store: store, Hosts: wider})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if src != FromStore || string(kept) != string(third) {
		t.Error("the reissued certificate was not kept")
	}
}
