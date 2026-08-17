//go:build linux

package daemon

import "testing"

func TestIssue254ProcessStatusGroupParser(t *testing.T) {
	status := []byte("Name:\tfixture\nGroups:\t4 27 991 1000\n")
	member, err := processStatusContainsGroup(status, 991)
	if err != nil {
		t.Fatal(err)
	}
	if !member {
		t.Fatal("expected group 991 membership")
	}
	member, err = processStatusContainsGroup(status, 992)
	if err != nil {
		t.Fatal(err)
	}
	if member {
		t.Fatal("unexpected group 992 membership")
	}
}

func TestIssue254ProcessStatusGroupParserRejectsMalformedGroup(t *testing.T) {
	_, err := processStatusContainsGroup([]byte("Groups:\t4 invalid 991\n"), 991)
	if err == nil {
		t.Fatal("expected malformed supplementary group error")
	}
}
