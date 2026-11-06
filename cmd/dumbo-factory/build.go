package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/logoscore/logos-golang-factory-core/pkg/factory"
	"github.com/logoscore/logos-golang-protocol/protocol"
)

// buildSchema is what Dumbo builds: PHP pages, bind-call minions the Dumbo
// channel calls. A page only answers, so it has one kind of connection; its
// commands say hello, move files both ways, a chunk a call (kept by core,
// or streamed through it), and list and remove them, to see how tasks and
// file transfers fit bind-call. Commands added later need no new version:
// values saved against the schema mean the same.
var buildSchema = protocol.BuildSchema{
	SchemaVersion: protocol.BuildSchemaV3,
	Version:       1,
	Channels: []protocol.BuildChannel{{
		// The Dumbo channel's connection schema, version 1: the method is
		// its built-in field, read from the endpoint; Dumbo adds none.
		ModuleName: channelModule, Versions: []int{1},
		Kinds: []protocol.BuildKind{{Kind: protocol.KindBindCall}},
	}},
	// Its pages encrypt with AES-256-CBC and HMAC-SHA256 (PHP's openssl);
	// core generates each build's keys and Dumbo writes them into the page.
	Crypto: protocol.CryptoAES256CBCHMACSHA256,
	Fields: []protocol.FieldSpec{
		{
			Key: "greeting", Type: protocol.FieldString, Label: "Greeting", Default: "hello from Dumbo", MaxLength: 200,
			Order: 1, Description: "What the page answers the hello command with.",
		},
	},
	Targets: []protocol.BuildTarget{{
		ID: targetPage, Label: "PHP page", Format: "script", Platforms: []string{"any/any"},
		Description: "A single PHP file to put on a web server (PHP 7.0 or later, with openssl). Core calls it through the Dumbo channel once a target names its URL.",
	}},
	Commands: []protocol.BuildCommand{
		{ID: "hello", Op: opHello, Label: "Say hello", Required: true, Description: "Answers with the build's greeting."},
		{
			ID: "download", Op: opDownload, Label: "Download a file", Default: true,
			Description: "Sends a file from the page's server to core, a chunk a call.",
			Result:      protocol.ResultFileDownload, Action: protocol.ActionDownloadFile,
			Params: []protocol.FieldSpec{{Key: "path", Num: 1, Type: protocol.FieldString, Label: "Path", Required: true}},
		},
		{
			ID: "upload", Op: opUpload, Label: "Upload a file", Default: true,
			Description: "Puts a file from core on the page's server, a chunk a call.",
			Result:      protocol.ResultFileUpload, Action: protocol.ActionUploadFile,
			Params: []protocol.FieldSpec{
				{Key: "file", Num: 1, Type: protocol.FieldFile, Label: "File", Required: true, Order: 1},
				{Key: "path", Num: 2, Type: protocol.FieldString, Label: "Path", Required: true, Order: 2,
					Description: "Where it goes on the server; a folder takes it under its own name."},
				{Key: "overwrite", Num: 3, Type: protocol.FieldBool, Label: "Overwrite", Default: false, Order: 3,
					Description: "Replace a file already there."},
			},
		},
		{
			ID: "stream_download", Op: opStreamDownload, Label: "Stream a file", Default: true,
			Description: "Streams a file of any size from the page's server to whoever downloads it, a chunk a call as the downloader takes them; core keeps none of it.",
			Result:      protocol.ResultFileStream, Action: protocol.ActionStreamFile,
			Params: []protocol.FieldSpec{{Key: "path", Num: 1, Type: protocol.FieldString, Label: "Path", Required: true}},
		},
		{
			ID: "stream_upload", Op: opStreamUpload, Label: "Stream a file up", Default: true,
			Description: "Puts a file of any size on the page's server, a chunk a call as it is uploaded; core keeps none of it.",
			Result:      protocol.ResultFileUpload, Action: protocol.ActionUploadStream,
			Params: []protocol.FieldSpec{
				{Key: "stream", Num: 1, Type: protocol.FieldStream, Label: "File", Order: 1,
					Description: "Picked in the browser, it goes once the task is queued. Left out, upload it with curl from the task's view."},
				{Key: "path", Num: 2, Type: protocol.FieldString, Label: "Path", Required: true, Order: 2,
					Description: "Where it goes on the server. A folder takes a picked file under its own name; with curl, name the file."},
				{Key: "overwrite", Num: 3, Type: protocol.FieldBool, Label: "Overwrite", Default: false, Order: 3,
					Description: "Replace a file already there."},
			},
		},
		{
			ID: "walk", Op: opWalk, Label: "Walk a directory", Default: true,
			Description: "Lists a directory and the folders under it, depth first, into core's file browser, across as many calls as it takes; links are not followed.",
			Action:      protocol.ActionWalkDir,
			Result:      protocol.ResultDirWalk,
			Params: []protocol.FieldSpec{
				{Key: "path", Num: 1, Type: protocol.FieldString, Label: "Path", Required: true, Order: 1},
				{Key: "depth", Num: 2, Type: protocol.FieldInt, Label: "Depth", Default: 5, Order: 2,
					Description: "How many levels of folders under it to go down; 0 for all."},
				{Key: "max_folders", Num: 3, Type: protocol.FieldInt, Label: "At most", Default: 10000, Order: 3,
					Description: "Folders to list before it stops; 0 for no limit (stop it from its task)."},
				{Key: "hidden", Num: 4, Type: protocol.FieldBool, Label: "Into hidden folders", Default: true, Order: 4},
				{Key: "one_fs", Num: 5, Type: protocol.FieldBool, Label: "Stay on its file system", Default: true, Order: 5,
					Description: "Folders on another file system (a mount, /proc) are not entered."},
			},
		},
		{
			ID: "ls", Op: opLs, Label: "List a directory", Default: true,
			Description: "Lists a folder on the page's server; a relative path is from the page's own folder.",
			Result:      protocol.ResultDirListing, Action: protocol.ActionListDir,
			Params: []protocol.FieldSpec{{Key: "path", Num: 1, Type: protocol.FieldString, Label: "Path", Default: "."}},
		},
		{
			ID: "rm", Op: opRm, Label: "Delete a file or folder", Default: true,
			Description: "Removes a file, a link (not what it points to) or a folder on the page's server; never a filesystem's root.",
			Action:      protocol.ActionDeletePath,
			Params: []protocol.FieldSpec{
				{Key: "path", Num: 1, Type: protocol.FieldString, Label: "Path", Required: true, Order: 1},
				{Key: "recursive", Num: 2, Type: protocol.FieldBool, Label: "With what is in it", Default: false, Order: 2,
					Description: "A folder that is not empty goes with everything in it."},
			},
		},
	},
}

const (
	channelModule = "dumbo"
	targetPage    = "php-page"
	// The commands' numbers; page.php's OP_HELLO, OP_DOWNLOAD, OP_UPLOAD,
	// OP_LS, OP_RM, OP_STREAM_DOWNLOAD, OP_STREAM_UPLOAD, OP_WALK.
	opHello          = 1
	opDownload       = 2
	opUpload         = 3
	opLs             = 4
	opRm             = 5
	opStreamDownload = 6
	opStreamUpload   = 7
	opWalk           = 8
)

//go:embed page.php
var pageTemplate string

// dumbo builds pages.
type dumbo struct {
	// version goes into the pages' hello (their implementation's version).
	version string
}

// The steps of a build.
var (
	stepRender = protocol.BuildStep{ID: "render", Label: "Write the page"}
	stepUpload = protocol.BuildStep{ID: "upload", Label: "Upload the artifact"}
)

// Steps is every build's plan.
func (d *dumbo) Steps(protocol.BuildRequest) []protocol.BuildStep {
	return []protocol.BuildStep{stepRender, stepUpload}
}

func (d *dumbo) Build(ctx context.Context, job *factory.Job) (factory.Result, error) {
	job.Step(stepRender.ID, "")
	page, conn, err := render(job.Request, d.version)
	if err != nil {
		return factory.Result{}, err
	}
	job.Logf("a PHP page for build %s, called through connection %d with %s, %d bytes",
		job.Request.BuildID, conn.place, conn.method, len(page))

	job.Step(stepUpload.ID, "")
	name := "dumbo-" + job.Request.BuildID[:8] + ".php"
	if err := job.UploadArtifact(ctx, name, "application/x-php", strings.NewReader(page)); err != nil {
		return factory.Result{}, err
	}
	return factory.Result{}, nil
}

// pageConn is the connection a page answers: its place in the build's
// connections, the method the channel calls with, and the largest message
// it carries (its channel's max_message, protocol.BuildConnection).
type pageConn struct {
	place  int
	method string
	limit  int
}

// sizes are what a page puts in one answer, for a connection that carries
// limit bytes a message: a file's chunk, a walk's listings, a listing's
// entries, a piece's entries. Over the protocol's limit they are the page's
// usual 512 KiB, 600 000 bytes, 5000 and 500.
func (c pageConn) sizes() (chunk, walk, entries, piece int) {
	chunk = min(protocol.MaxChunkData, c.limit*2/3)
	walk = min(600000, c.limit*4/5)
	// An entry takes some 100 bytes; an ls answers in one go.
	entries = max(1, min(5000, c.limit/150))
	piece = max(1, min(500, c.limit/400))
	return chunk, walk, entries, piece
}

// bindConn is the build's first bind-call connection through the Dumbo
// channel: a page answers one.
func bindConn(req protocol.BuildRequest) (pageConn, error) {
	for i, c := range req.Connections {
		if c.ConnectionKind() != protocol.KindBindCall || c.Endpoint.Channel.ModuleName != channelModule {
			continue
		}
		method, _ := c.Endpoint.Params["method"].(string)
		if method == "" {
			method = "POST"
		}
		switch method {
		case "POST", "PUT", "PATCH":
		default:
			return pageConn{}, factory.Fail("bad_method", fmt.Sprintf("connection %d: method %q is not one the Dumbo channel calls with", i+1, method))
		}
		return pageConn{place: i + 1, method: method, limit: c.MessageLimit()}, nil
	}
	return pageConn{}, factory.Fail("no_bind_connection", "the build has no bind-call connection through the Dumbo channel")
}

// render is the page a request asks for. The keys go in as base64 and every
// value in a single-quoted PHP string.
func render(req protocol.BuildRequest, version string) (string, pageConn, error) {
	conn, err := bindConn(req)
	if err != nil {
		return "", conn, err
	}
	if req.Crypto == nil || req.Crypto.Suite != protocol.CryptoAES256CBCHMACSHA256 {
		return "", conn, factory.Fail("no_keys", "the build request has no "+protocol.CryptoAES256CBCHMACSHA256+" keys")
	}
	var keys protocol.AES256CBCHMACSHA256Material
	if err := json.Unmarshal(req.Crypto.Material, &keys); err != nil || len(keys.EncKey) != 32 || len(keys.MACKey) != 32 {
		return "", conn, factory.Fail("no_keys", "the build's keys do not read")
	}
	if len(req.BuildID) < 8 {
		return "", conn, factory.Fail("bad_build_id", "the build id is too short")
	}
	greeting, _ := req.Values["greeting"].(string)
	id8 := strings.ReplaceAll(req.BuildID, "-", "")[:8]
	chunk, walk, entries, piece := conn.sizes()
	page := strings.NewReplacer(
		"__BUILD_ID__", req.BuildID,
		"__ID8__", id8,
		"__METHOD__", conn.method,
		"__ENC_KEY__", base64.StdEncoding.EncodeToString(keys.EncKey),
		"__MAC_KEY__", base64.StdEncoding.EncodeToString(keys.MACKey),
		"__CONN__", strconv.Itoa(conn.place),
		"__CHUNK__", strconv.Itoa(chunk),
		"__WALK_BYTES__", strconv.Itoa(walk),
		"__MAX_ENTRIES__", strconv.Itoa(entries),
		"__PIECE__", strconv.Itoa(piece),
		"__GREETING__", phpQuote(greeting),
		"__VERSION__", phpQuote(version),
	).Replace(pageTemplate)
	return page, conn, nil
}

// phpQuote escapes s for a single-quoted PHP string.
func phpQuote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}
