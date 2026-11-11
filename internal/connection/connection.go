// Package connection declares the channel's connection schema: the one
// kind it carries, bind-call, with what is built into a page (Fields) and
// what operators fill in for each page they put on a server (Target).
package connection

import (
	"github.com/logoscore/logos-golang-protocol/protocol"
)

// Version is the built-in fields' version: raise it when pages built
// against the current fields would read new ones wrongly. Target fields
// change without it: nothing is built from them.
const Version = 1

// Header names are RFC 9110 tokens; values must not break the header line.
const (
	headerNamePattern  = `^[!#$%&'*+.^_|~0-9A-Za-z-]+$`
	headerValuePattern = `^[^\r\n]*$`
)

// Methods a page can be called with.
var Methods = []string{"POST", "PUT", "PATCH"}

// Schema is the connection schema declared at registration.
func Schema() protocol.ConnectionSchema {
	methods := make([]protocol.EnumOption, len(Methods))
	for i, m := range Methods {
		methods[i] = protocol.EnumOption{Value: m, Label: m}
	}
	return protocol.ConnectionSchema{
		SchemaVersion: protocol.ConnectionSchemaV2,
		Version:       Version,
		Kinds: map[string]protocol.ConnectionKind{
			protocol.KindBindCall: {
				Fields: []protocol.FieldSpec{{
					Key: "method", Num: 1, Type: protocol.FieldEnum, Label: "Method",
					Description: "The request method the channel calls the page with. The page answers only this one: any other request gets a plain 404, as a missing page would.",
					Group:       "Request", Order: 1, Default: "POST", Options: methods,
				}},
				Target: []protocol.FieldSpec{
					{
						Key: "url", Num: 2, Type: protocol.FieldURL, Label: "Page URL", Required: true,
						Description: "Where the page is: http(s)://host/path/page.php, as a browser would ask for it.",
						Group:       "Address", Order: 1, Placeholder: "https://www.example.com/wp-content/uploads/cache.php",
						Pattern: `^https?://[^\s]+$`, MaxLength: 2048,
					},
					{
						Key: "host_header", Num: 3, Type: protocol.FieldString, Label: "Host header",
						Description: "Host header to send when it must differ from the URL's host (a CDN, a virtual host reached by address).",
						Group:       "Address", Order: 2, MaxLength: 255,
					},
					{
						Key: "proxy", Num: 4, Type: protocol.FieldURL, Label: "Proxy",
						Description: "Proxy the channel calls the page through, e.g. http://proxy:3128 or socks5://127.0.0.1:1080.",
						Group:       "Address", Order: 3, MaxLength: 2048,
					},
					{
						Key: "tls_verify", Num: 5, Type: protocol.FieldBool, Label: "Verify TLS certificate",
						Description: "Check the server's certificate against the channel's trusted roots.",
						Group:       "TLS", Order: 4, Default: false,
					},
					{
						Key: "user_agent", Num: 6, Type: protocol.FieldString, Label: "User-Agent",
						Description: "User-Agent to send. Empty: a common browser's.",
						Group:       "Request", Order: 5, MaxLength: 512, Format: protocol.FormatUserAgent,
					},
					{
						Key: "headers", Num: 7, Type: protocol.FieldStringMap, Label: "Extra headers",
						Description: "Headers added to every call, by name (a cookie the site needs, say).",
						Group:       "Request", Order: 6,
						KeyPattern: headerNamePattern, Pattern: headerValuePattern, MaxLength: 4096, MaxItems: 32,
					},
				},
			},
		},
	}
}
