package protocol

import (
	"testing"

	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
)

func TestContractVersions(t *testing.T) {
	if ProtocolVersion != 2 || Handshake.ProtocolVersion != ProtocolVersion || FirstCoreVersion != "0.1.304" {
		t.Fatalf("protocol %d, handshake %d", ProtocolVersion, Handshake.ProtocolVersion)
	}
	if got := runtimecontract.HostAPIVersion; HostAPIVersion != 10 || got != 10 {
		t.Fatalf("host api %d, runtime contract %d", HostAPIVersion, got)
	}
}

func TestOutdatedContractMessage(t *testing.T) {
	// A later core of the protocol still sends the operator to the first
	// one: a plugin built for it works.
	got := OutdatedContractMessage("demo", "1.2.3", "0.1.310")
	want := "plugin demo@1.2.3 was built for an older plugin protocol and cannot be loaded by this core (0.1.310, plugin protocol 2); install a version built for core 0.1.304 or later"
	if got != want {
		t.Fatalf("got %q", got)
	}
}
