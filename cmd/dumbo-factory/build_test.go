package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/logoscore/logos-golang-factory-core/pkg/factorytest"
	"github.com/logoscore/logos-golang-module-core/pkg/mgmt"
	"github.com/logoscore/logos-golang-protocol/protocol"
)

var testKeys = protocol.AES256CBCHMACSHA256Material{EncKey: bytes.Repeat([]byte{1}, 32), MACKey: bytes.Repeat([]byte{2}, 32)}

// request is a build of a page called with method through the Dumbo
// channel: two connections of it, the page answering the first.
func request(t *testing.T, method string) protocol.BuildRequest {
	t.Helper()
	material, err := json.Marshal(testKeys)
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]any{}
	if method != "" {
		params["method"] = method
	}
	return protocol.BuildRequest{
		BuildID: "5b0c4f6e-8a1d-4c2b-9e7f-3d2a1b0c9e8f", Target: targetPage, Platform: "any/any", SchemaVersion: 1,
		Values:   map[string]any{"greeting": `it's a \ page`},
		Commands: []string{"hello"},
		Connections: []protocol.BuildConnection{
			{Endpoint: protocol.ChannelEndpointExport{Format: "logos.channel-endpoint.v2", Name: "page",
				Channel: protocol.ExportChannel{ModuleName: channelModule, SchemaVersion: 1}, Kind: protocol.KindBindCall, Params: params}},
			{Endpoint: protocol.ChannelEndpointExport{Format: "logos.channel-endpoint.v2", Name: "second",
				Channel: protocol.ExportChannel{ModuleName: channelModule, SchemaVersion: 1}, Kind: protocol.KindBindCall,
				Params: map[string]any{"method": "PATCH"}}},
		},
		Crypto: &protocol.BuildCrypto{Suite: protocol.CryptoAES256CBCHMACSHA256, Material: material},
	}
}

func TestSchema(t *testing.T) {
	if err := protocol.ValidateBuildSchema(buildSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := mgmt.New(manifest()); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	for name, svg := range map[string]string{"icon": icon, "iconLight": iconLight} {
		if err := protocol.ValidateIcon(svg); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The page's ops are the schema's.
	for name, op := range map[string]int{"OP_HELLO": opHello, "OP_DOWNLOAD": opDownload, "OP_UPLOAD": opUpload, "OP_LS": opLs, "OP_RM": opRm,
		"OP_STREAM_DOWNLOAD": opStreamDownload, "OP_STREAM_UPLOAD": opStreamUpload, "OP_WALK": opWalk} {
		if !strings.Contains(pageTemplate, fmt.Sprintf("const %s = %d;", name, op)) {
			t.Errorf("page.php and the schema disagree on %s", name)
		}
	}
	ops := map[string]int{}
	for _, c := range buildSchema.Commands {
		ops[c.ID] = c.Op
	}
	if ops["hello"] != opHello || ops["download"] != opDownload || ops["upload"] != opUpload || ops["ls"] != opLs || ops["rm"] != opRm ||
		ops["stream_download"] != opStreamDownload || ops["stream_upload"] != opStreamUpload || ops["walk"] != opWalk {
		t.Errorf("the schema's ops %v", ops)
	}
}

func TestBuild(t *testing.T) {
	store := factorytest.NewStore(t)
	out := factorytest.Run(t, buildSchema, &dumbo{version: "v9"}, store, request(t, "PUT"))
	if out.Result.Status != protocol.BuildSucceeded {
		t.Fatalf("result: %+v", out.Result)
	}
	page := string(out.Artifact)
	for _, want := range []string{
		"const METHOD = 'PUT';", "const CONN = 1;", `const GREETING = 'it\'s a \\ page';`,
		"const VERSION = 'dumbo-page/v9';", "class DumboPage5b0c4f6e", "build 5b0c4f6e-8a1d-4c2b-9e7f-3d2a1b0c9e8f",
		"const ENC_KEY = 'AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=';",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(page, "__") && strings.Contains(page, "__METHOD__") {
		t.Error("a placeholder left in the page")
	}
	if got := out.Result.Artifact; got == nil || got.Filename != "dumbo-5b0c4f6e.php" {
		t.Errorf("artifact %+v", got)
	}
}

func TestBuildDefaultsToPost(t *testing.T) {
	_, page := factorytest.Build(t, buildSchema, &dumbo{version: "v9"}, factorytest.NewStore(t), request(t, ""))
	if !strings.Contains(string(page), "const METHOD = 'POST';") {
		t.Fatal("a connection without a method is called with POST")
	}
}

func TestBuildFails(t *testing.T) {
	cases := map[string]func(*protocol.BuildRequest){
		"bad_method": func(r *protocol.BuildRequest) { r.Connections[0].Endpoint.Params["method"] = "GET" },
		"no_keys":    func(r *protocol.BuildRequest) { r.Crypto.Material = json.RawMessage(`{}`) },
	}
	for code, change := range cases {
		t.Run(code, func(t *testing.T) {
			req := request(t, "POST")
			change(&req)
			res, page := factorytest.Build(t, buildSchema, &dumbo{version: "v9"}, factorytest.NewStore(t), req)
			if res.Status == protocol.BuildSucceeded || res.Error == nil || res.Error.Code != code || page != nil {
				t.Fatalf("result %+v", res)
			}
		})
	}
}

// A build core lets through always has a Dumbo connection; bindConn still
// says so when there is none, rather than writing a page nothing calls.
func TestBindConnNone(t *testing.T) {
	req := request(t, "POST")
	req.Connections = nil
	if _, err := bindConn(req); err == nil || !strings.Contains(err.Error(), "no_bind_connection") {
		t.Fatalf("no connection: %v", err)
	}
}
