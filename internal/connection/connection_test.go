package connection

import (
	"testing"

	"github.com/logoscore/logos-golang-protocol/protocol"
)

func TestSchemaIsValid(t *testing.T) {
	s := Schema()
	if err := protocol.ValidateConnectionSchema(s); err != nil {
		t.Fatal(err)
	}
	if kinds := s.SupportedKinds(); len(kinds) != 1 || kinds[0] != protocol.KindBindCall {
		t.Fatalf("kinds %v", kinds)
	}
	if p := protocol.ValidateTargetValues(s, protocol.KindBindCall, map[string]any{"url": "https://h/a.php"}); p != nil {
		t.Fatalf("a target with its URL: %v", p)
	}
	if p := protocol.ValidateTargetValues(s, protocol.KindBindCall, map[string]any{}); p["url"] == "" {
		t.Fatalf("a target without its URL: %v", p)
	}
}

// Every field has a number, the target's sharing the kind's numbering: each
// stays its field's (docs: minion-connection, Field numbers).
func TestFieldNumbers(t *testing.T) {
	want := map[string]int{"method": 1, "url": 2, "host_header": 3, "proxy": 4, "tls_verify": 5, "user_agent": 6, "headers": 7}
	for kind, k := range Schema().Kinds {
		for _, f := range append(append([]protocol.FieldSpec{}, k.Fields...), k.Target...) {
			if f.Num != want[f.Key] {
				t.Errorf("%s: %s is %d, want %d", kind, f.Key, f.Num, want[f.Key])
			}
		}
	}
}
