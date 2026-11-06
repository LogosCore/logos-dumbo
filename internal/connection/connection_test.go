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
