package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/logoscore/logos-dumbo/internal/caller"
	"github.com/logoscore/logos-golang-channel-core/v3/pkg/linkclient"
	"github.com/logoscore/logos-golang-protocol/protocol"
)

// The two halves together: the channel's caller calls a page this factory
// built, under real PHP, as core's calls go through the Dumbo channel. The
// page answers the caller's request in the form the caller takes (200, an
// octet stream, the id and the sealed answer), and anything else the
// caller takes for not the minion.
func TestChannelCallsThePage(t *testing.T) {
	req := request(t, "PUT")
	url := startPage(t, req)
	var buildID [16]byte
	copy(buildID[:], mustUUID(t, req.BuildID))

	msgID := protocol.NewMessageID()
	raw, err := protocol.MarshalCBOR(protocol.RehelloItem{T: protocol.ItemRehello})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := protocol.MarshalCBOR(protocol.CoreMessage{V: protocol.MinionMessageV3, ID: msgID, Items: []cbor.RawMessage{raw},
		At: uint64(time.Now().Unix())})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := protocol.SealFrame(testKeys, buildID, protocol.FrameCoreToMinion, plain)
	if err != nil {
		t.Fatal(err)
	}
	c := caller.New(caller.Settings{Timeout: 20 * time.Second, MaxBodyBytes: 1 << 20})
	ctx := context.Background()

	ans := c.Call(ctx, linkclient.Call{Request: 1, Values: map[string]any{"method": "PUT", "url": url}, ID: buildID, Data: sealed})
	if ans.Failure != nil || ans.ID == nil || *ans.ID != buildID || ans.Status != http.StatusOK {
		t.Fatalf("answer %+v", ans)
	}
	opened, err := protocol.OpenFrame(testKeys, buildID, protocol.FrameMinionToCore, ans.Data)
	if err != nil {
		t.Fatalf("the answer does not open: %v", err)
	}
	var msg protocol.MinionMessage
	if err := protocol.UnmarshalCBOR(opened, &msg); err != nil || !bytes.Equal(msg.Re, msgID) {
		t.Fatalf("the answer: %v (re % x)", err, msg.Re)
	}
	hello := false
	for _, it := range msg.Items {
		hello = hello || protocol.ItemType(it) == protocol.ItemHello
	}
	if !hello {
		t.Fatal("no hello in the answer")
	}

	// Another method than the page's, or another page: not the minion.
	for name, values := range map[string]map[string]any{
		"method": {"method": "POST", "url": url},
		"page":   {"method": "PUT", "url": strings.Replace(url, "page.php", "nothere.php", 1)},
	} {
		a := c.Call(ctx, linkclient.Call{Request: 2, Values: values, ID: buildID, Data: sealed})
		if a.Failure != nil || a.ID != nil {
			t.Errorf("%s: %+v", name, a)
		}
	}
}
