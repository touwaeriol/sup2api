package gateway

// The one part of a relayed request the core carries out for the plugin,
// looked up by name with no vendor logic here: response_stream, a response
// body in a format other than SSE or JSON, decoded into SSE
// (sdk/awseventstream), after which relaying, metering and conversion handle
// it like any event stream. A name stands for the framing and the shape of
// the events in it, not for the framing alone. Plugins sign their requests
// themselves (sdk/reqsign); the core sends the bytes it is given.

import (
	"io"
	"mime"
	"net/http"

	"github.com/Sub2API-Devs/sup2api/next/sdk/awseventstream"
)

// responseStream is a response format the core turns into SSE.
type responseStream struct {
	// contentType is the media type the upstream answers with.
	contentType string
	// sse returns the SSE form of a body in this framing.
	sse func(io.Reader) io.Reader
}

// responseStreams maps UpstreamRequest.response_stream names.
var responseStreams = map[string]responseStream{
	// Chunk events only (Bedrock's InvokeModelWithResponseStream); the
	// adapter ends a stream on an exception with an Anthropic-shaped error
	// event.
	awseventstream.FormatChunk: {
		contentType: awseventstream.ContentType,
		sse:         func(r io.Reader) io.Reader { return awseventstream.NewChunkSSEReader(r) },
	},
}

// adaptResponseStream presents a successful answer in the framing the plugin
// named as an event stream. An answer with another content type (a JSON
// body, plain SSE) is left as it is.
func adaptResponseStream(resp *http.Response, name string) {
	format, ok := responseStreams[name]
	if !ok {
		return
	}
	if ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); ct != format.contentType {
		return
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{format.sse(resp.Body), resp.Body}
	resp.Header.Set("Content-Type", "text/event-stream")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
}
