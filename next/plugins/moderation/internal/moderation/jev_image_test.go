package moderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test images are drawn here; there are no binary fixtures.

func b64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

func encodePNG(t testing.TB, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return b64(buf.Bytes())
}

func encodeJPEG(t testing.TB, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return b64(buf.Bytes())
}

// filled is a w×h opaque image of one colour.
func filled(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

// noise is a w×h image no encoder can compress.
func noise(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(r.Uint32())
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	return img
}

// decodeAsset decodes what would be sent and checks the declared format.
func decodeAsset(t *testing.T, a *typesafeAsset) image.Image {
	t.Helper()
	if a == nil {
		t.Fatal("image was skipped")
	}
	data, err := base64.StdEncoding.DecodeString(a.DataBase64)
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || format != a.Format || a.Type != "image" {
		t.Fatalf("asset %s / %s: decoded as %s, %v", a.Type, a.Format, format, err)
	}
	return img
}

// near reports whether c is within tol of the 8-bit colour want.
func near(c color.Color, want color.RGBA, tol int) bool {
	r, g, b, _ := c.RGBA()
	d := func(x uint32, y uint8) bool { v := int(x>>8) - int(y); return v >= -tol && v <= tol }
	return d(r, want.R) && d(g, want.G) && d(b, want.B)
}

var (
	white = color.RGBA{0xff, 0xff, 0xff, 0xff}
	red   = color.RGBA{0xff, 0, 0, 0xff}
	blue  = color.RGBA{0, 0, 0xff, 0xff}
)

// inLimits reports whether System One accepts an image of this size.
func inLimits(b image.Rectangle) bool {
	w, h := b.Dx(), b.Dy()
	return min(w, h) >= jevImageMinSide && max(w, h) <= jevImageMaxSide && w*h >= jevImageMinPixels && w*h <= jevImageMaxPixels
}

func TestJevLongContextHeader(t *testing.T) {
	var mu sync.Mutex
	var headers []http.Header
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		headers, bodies = append(headers, r.Header.Clone()), append(bodies, string(body))
		mu.Unlock()
		var req struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		_ = json.Unmarshal(body, &req)
		answers := map[string]any{}
		for c := range req.Questions {
			answers[c] = map[string]any{"type": "noul", "noul": 0.01}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-mock", "answers": answers})
	}))
	defer srv.Close()
	def, _ := DefaultQuestions(nil)
	a, err := compileAuditor(1, "j", AuditorTypeSafe, 1, AuditorConfig{}, nil, &def)
	if err != nil {
		t.Fatal(err)
	}
	ch := &liveChannel{Type: ChannelTypeSafe, BaseURL: srv.URL, Key: "k", Model: "jev"}
	ctx := context.Background()
	if res, err := typesafeAudit(ctx, ch, a.questions, true, &Input{Text: "text only"}); err != nil || res.SentImages != 0 {
		t.Fatalf("text only: %+v %v", res, err)
	}
	// One image inside the limits and one redrawn to fit them.
	in := &Input{Text: "with images", Images: []Image{{Data: encodePNG(t, filled(300, 300, red))}, {Data: encodePNG(t, filled(100, 50, blue))}}}
	if res, err := typesafeAudit(ctx, ch, a.questions, true, in); err != nil || res.SentImages != 2 || res.SkippedImages != 0 {
		t.Fatalf("with images: %+v %v", res, err)
	}
	if len(headers) != 2 {
		t.Fatalf("calls = %d", len(headers))
	}
	for i, h := range headers {
		if h.Get(jevLongContextHeader) != "true" || h.Get("TypeSafe-Danger-Zone-Long-Context") != "true" {
			t.Fatalf("call %d has no long-context header: %v", i, h)
		}
	}
	if headers[0].Get(jevImagesHeader) != "" || headers[1].Get(jevImagesHeader) != "true" {
		t.Fatalf("image header: text only %q, with images %q", headers[0].Get(jevImagesHeader), headers[1].Get(jevImagesHeader))
	}
	if strings.Contains(bodies[0], `"assets"`) || strings.Count(bodies[1], `"data_base64"`) != 2 {
		t.Fatal("assets of the two calls")
	}
}

func TestJevAssetInLimitsSentAsIs(t *testing.T) {
	raw := encodePNG(t, filled(1000, 1000, red))
	a := jevAsset(context.Background(), raw)
	if a == nil || a.DataBase64 != raw || a.Format != "png" || a.tokens != jevImageTokens(1000, 1000) {
		t.Fatalf("in-limits PNG was not sent as it is: %+v", a)
	}
	raw = encodeJPEG(t, filled(1568, 280, red))
	if a := jevAsset(context.Background(), raw); a == nil || a.DataBase64 != raw || a.Format != "jpeg" {
		t.Fatal("in-limits JPEG was not sent as it is")
	}
}

func TestJevNormalizeOversized(t *testing.T) {
	a := jevAsset(context.Background(), encodeJPEG(t, filled(4000, 2000, red)))
	img := decodeAsset(t, a)
	if b := img.Bounds(); b.Dx() != jevImageMaxSide || b.Dy() != jevImageMaxSide/2 || !inLimits(b) {
		t.Fatalf("4000x2000 became %v", b)
	}
	if a.tokens != jevImageTokens(1568, 784) || !near(img.At(784, 392), red, 8) {
		t.Fatalf("tokens %d, centre %v", a.tokens, img.At(784, 392))
	}
	// The image is scaled in bands of rows: a top-to-bottom gradient stays
	// smooth across them and keeps its ends.
	grad := image.NewGray(image.Rect(0, 0, 2400, 3000))
	for y := range 3000 {
		for x := range 2400 {
			grad.SetGray(x, y, color.Gray{uint8(y * 255 / 2999)})
		}
	}
	img = decodeAsset(t, jevAsset(context.Background(), encodePNG(t, grad)))
	if b := img.Bounds(); b.Dx() != 1254 || b.Dy() != 1568 {
		t.Fatalf("2400x3000 became %v", b)
	}
	for y := range 1568 {
		got, _, _, _ := img.At(600, y).RGBA()
		if want := y * 255 / 1567; int(got>>8) < want-1 || int(got>>8) > want+1 {
			t.Fatalf("row %d is %d, want %d", y, got>>8, want)
		}
	}
	// Never scaled up: a small side is padded, the long side still shrinks.
	img = decodeAsset(t, jevAsset(context.Background(), encodePNG(t, filled(3136, 200, red))))
	if b := img.Bounds(); b.Dx() != 1568 || b.Dy() != 280 || !near(img.At(784, 140), red, 0) || !near(img.At(784, 5), white, 0) {
		t.Fatalf("3136x200 became %v", b)
	}
}

func TestJevNormalizeTooSmall(t *testing.T) {
	a := jevAsset(context.Background(), encodePNG(t, filled(100, 50, blue)))
	img := decodeAsset(t, a)
	b := img.Bounds()
	if b.Dx() != 280 || b.Dy() != 280 || a.Format != "png" || a.tokens != jevImageTokens(280, 280) {
		t.Fatalf("100x50 became %v %s", b, a.Format)
	}
	for _, p := range []image.Point{{0, 0}, {279, 0}, {0, 279}, {279, 279}, {89, 140}, {140, 114}} {
		if !near(img.At(p.X, p.Y), white, 0) {
			t.Fatalf("%v is %v, want white", p, img.At(p.X, p.Y))
		}
	}
	// The source sits centred and unscaled: x 90..189, y 115..164.
	for _, p := range []image.Point{{90, 115}, {189, 164}, {140, 140}} {
		if !near(img.At(p.X, p.Y), blue, 0) {
			t.Fatalf("%v is %v, want the source colour", p, img.At(p.X, p.Y))
		}
	}
}

func TestJevNormalizeTransparency(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2000, 1000))
	draw.Draw(src, image.Rect(1000, 0, 2000, 1000), image.NewUniform(red), image.Point{}, draw.Src) // left half stays transparent
	img := decodeAsset(t, jevAsset(context.Background(), encodePNG(t, src)))
	if b := img.Bounds(); b.Dx() != 1568 || b.Dy() != 784 {
		t.Fatalf("bounds %v", b)
	}
	if !near(img.At(300, 400), white, 0) || !near(img.At(1300, 400), red, 0) {
		t.Fatalf("transparent %v, opaque %v", img.At(300, 400), img.At(1300, 400))
	}
	if _, _, _, alpha := img.At(300, 400).RGBA(); alpha != 0xffff {
		t.Fatalf("alpha kept: %d", alpha)
	}
	// Transparency inside the limits takes the unscaled path of a small image.
	small := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	img = decodeAsset(t, jevAsset(context.Background(), encodePNG(t, small)))
	if !near(img.At(140, 140), white, 0) {
		t.Fatalf("small transparent image: %v", img.At(140, 140))
	}
}

// tinyWebP is a 1×1 lossless WebP.
var tinyWebP = []byte("RIFF\x1a\x00\x00\x00WEBPVP8L\x0d\x00\x00\x00\x2f\x00\x00\x00\x10\x07\x10\x11\x11\x88\x88\xfe\x07\x00")

func TestJevNormalizeGIFAndWebP(t *testing.T) {
	pal := color.Palette{white, red, blue}
	frame := func(c uint8) *image.Paletted {
		f := image.NewPaletted(image.Rect(0, 0, 400, 300), pal)
		for i := range f.Pix {
			f.Pix[i] = c
		}
		return f
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{frame(1), frame(2)}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	// A GIF is redrawn even inside the limits: System One takes PNG and JPEG.
	a := jevAsset(context.Background(), b64(buf.Bytes()))
	img := decodeAsset(t, a)
	if b := img.Bounds(); a.Format != "png" || b.Dx() != 400 || b.Dy() != 300 || !near(img.At(200, 150), red, 0) {
		t.Fatalf("gif: %s %v %v (want the first frame)", a.Format, b, img.At(200, 150))
	}
	a = jevAsset(context.Background(), b64(tinyWebP))
	img = decodeAsset(t, a)
	if b := img.Bounds(); (a.Format != "png" && a.Format != "jpeg") || b.Dx() != 280 || b.Dy() != 280 {
		t.Fatalf("webp: %s %v", a.Format, b)
	}
}

func TestJevNormalizeFallsBackToJPEG(t *testing.T) {
	raw := encodePNG(t, noise(2000, 1600))
	a := jevAsset(context.Background(), raw)
	img := decodeAsset(t, a)
	if b := img.Bounds(); a.Format != "jpeg" || b.Dx() != 1568 || b.Dy() != 1254 {
		t.Fatalf("noise: %s %v", a.Format, b)
	}
	// The PNG of the same canvas is what was over the limit.
	if n := base64.StdEncoding.DecodedLen(len(encodePNG(t, noise(1568, 1254)))); n <= jevImagePNGMax {
		t.Fatalf("the noise PNG is only %d bytes", n)
	}
	if a := jevAsset(context.Background(), encodePNG(t, filled(2000, 1600, red))); a == nil || a.Format != "png" {
		t.Fatal("a small PNG must stay a PNG")
	}
}

func TestJevAssetSkips(t *testing.T) {
	ctx := context.Background()
	ok := encodePNG(t, filled(300, 300, red))
	okData, _ := base64.StdEncoding.DecodeString(ok)
	exact := encodePNG(t, image.NewGray(image.Rect(0, 0, 4000, 3000))) // exactly 12 MP
	cases := map[string]string{
		"url":            "https://example.com/a.png",
		"broken base64":  ok[:len(ok)/2] + "!!!" + ok[len(ok)/2:],
		"over 16 MiB":    b64(append(bytes.Clone(okData), make([]byte, jevImageMaxBytes)...)),
		"over 12 MP":     encodePNG(t, image.NewGray(image.Rect(0, 0, 4000, 3001))),
		"unknown format": b64([]byte("BM this is not an image the engine decodes")),
		"empty":          "",
	}
	for name, raw := range cases {
		if a := jevAsset(ctx, raw); a != nil {
			t.Errorf("%s: sent as %s (%d bytes of base64)", name, a.Format, len(a.DataBase64))
		}
	}
	// The limits themselves pass: 16 MiB decoded, 12 MP.
	atLimit := b64(append(bytes.Clone(okData), make([]byte, jevImageMaxBytes-len(okData))...))
	if a := jevAsset(ctx, atLimit); a == nil || a.DataBase64 != atLimit {
		t.Error("an image of exactly 16 MiB was skipped")
	}
	if img := decodeAsset(t, jevAsset(ctx, exact)); img.Bounds().Dx() != 1568 || img.Bounds().Dy() != 1176 {
		t.Errorf("a 12 MP image became %v", img.Bounds())
	}
	// A cut image that needs redrawing fails in the decoder and is skipped.
	small := encodePNG(t, filled(100, 100, red))
	smallData, _ := base64.StdEncoding.DecodeString(small)
	if a := jevAsset(ctx, b64(smallData[:len(smallData)-20])); a != nil {
		t.Error("an undecodable image that needs redrawing was sent")
	}
	// URL images of the input are never sent, whatever their data.
	if as := jevAssets(ctx, &Input{Images: []Image{{URL: "https://example.com/a.png", Data: ok}}}); len(as) != 0 {
		t.Error("a URL image was sent")
	}
}

func TestJevAssetsDedupeAndBudget(t *testing.T) {
	ctx := context.Background()
	// The same picture as a PNG too small and as that PNG again: both are
	// redrawn to the same bytes and sent once; so is a repeated in-limits
	// image.
	small, ok := encodePNG(t, filled(100, 100, red)), encodePNG(t, filled(300, 300, red))
	smallGray := encodePNG(t, filled(100, 100, color.RGBA{0x80, 0x80, 0x80, 0xff}))
	in := &Input{Text: "t", Images: []Image{{Data: small}, {Data: ok}, {Data: small}, {Data: smallGray}, {Data: ok}}}
	as := jevAssets(ctx, in)
	if len(as) != 3 {
		t.Fatalf("assets = %d, want 3 (two duplicates dropped)", len(as))
	}
	if as[1].DataBase64 != ok || as[0].DataBase64 == as[2].DataBase64 {
		t.Fatal("order or content of the deduplicated assets")
	}
	// An asset stands for the image it was made of and for its duplicates:
	// the upstream receives their content with it.
	if !slices.Equal(as[0].from, []int{0, 2}) || !slices.Equal(as[1].from, []int{1, 4}) || !slices.Equal(as[2].from, []int{3}) {
		t.Fatalf("assets of the images %v, %v, %v", as[0].from, as[1].from, as[2].from)
	}
	// Different encodings of the same pixels normalize to one asset.
	var plain bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&plain, filled(100, 100, red)); err != nil {
		t.Fatal(err)
	}
	if b64(plain.Bytes()) == small {
		t.Fatal("the two encodings are the same bytes")
	}
	if as := jevAssets(ctx, &Input{Images: []Image{{Data: small}, {Data: b64(plain.Bytes())}}}); len(as) != 1 {
		t.Fatalf("same picture, two encodings: %d assets", len(as))
	}

	// 12000 runes of text and eight different 1024² images fit 200k tokens.
	text := strings.Repeat("字", 12000)
	var images []Image
	for i := range 8 {
		images = append(images, Image{Data: encodePNG(t, filled(1024, 1024, color.RGBA{uint8(i * 30), 0, 0, 0xff}))})
	}
	as = jevAssets(ctx, &Input{Text: text, Images: images})
	tokens := 12000
	for _, a := range as {
		tokens += a.tokens
	}
	if len(as) != 8 || tokens > jevRequestTokenLimit || tokens <= 24_000 {
		t.Fatalf("assets = %d, tokens = %d", len(as), tokens)
	}
	// The budget still stops the packing: 200k tokens hold 20 images of
	// 1568² (9934 tokens each), not 21.
	images = images[:0]
	for i := range 21 {
		images = append(images, Image{Data: encodePNG(t, filled(1568, 1568, color.RGBA{0, uint8(i * 10), 0, 0xff}))})
	}
	as = jevAssets(ctx, &Input{Images: images})
	if len(as) != 20 {
		t.Fatalf("budget: %d assets", len(as))
	}
	// The image beyond the budget is named by no asset.
	for _, a := range as {
		if slices.Contains(a.from, 20) {
			t.Fatal("the image beyond the budget counts as sent")
		}
	}
}

// busySlots takes every redraw slot of the Jev auditor, as large images of
// other requests would; free gives them back, and so does the end of the
// test.
func busySlots(t *testing.T) (free func()) {
	t.Helper()
	for range cap(jevNormalizeSlots) {
		jevNormalizeSlots <- struct{}{}
	}
	free = sync.OnceFunc(func() {
		for range cap(jevNormalizeSlots) {
			<-jevNormalizeSlots
		}
	})
	t.Cleanup(free)
	return free
}

func TestJevNormalizeSlotsHonourContext(t *testing.T) {
	release := busySlots(t)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	small := encodePNG(t, filled(100, 100, red))
	done := make(chan *typesafeAsset, 1)
	go func() { done <- jevAsset(ctx, small) }()
	select {
	case a := <-done:
		if a != nil {
			t.Fatal("an image was redrawn without a slot")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("jevAsset blocked on the slots with a canceled context")
	}
	// An image inside the limits needs no slot.
	if a := jevAsset(ctx, encodePNG(t, filled(300, 300, red))); a == nil {
		t.Fatal("an in-limits image waited for a slot")
	}
	release()
	if a := jevAsset(context.Background(), small); a == nil {
		t.Fatal("slots were not released")
	}
	if len(jevNormalizeSlots) != 0 {
		t.Fatalf("%d slots still taken", len(jevNormalizeSlots))
	}
}

func TestRulesVersionLegacyHashes(t *testing.T) {
	if builtinVersions[legacyWireHashEN] != RulesVersionEN || builtinVersions[legacyWireHashZH] != RulesVersionZH13 ||
		builtinVersions[legacyWireHashZHv2] != RulesVersionZHv2 || builtinVersions[legacyWireHashZHv3] != RulesVersionZHv3 ||
		len(builtinVersions) != 5 {
		t.Fatalf("builtinVersions = %v", builtinVersions)
	}
	if RulesVersionEN != "content-audit-13-en-v1" || RulesVersionZH13 != "content-audit-13-zh-v1" ||
		legacyWireHashEN != "e2ca199d2bb8208d2ebdc47dbf079b49f9f41794c9a4a25527fb1ec609451add" ||
		legacyWireHashZH != "b01b9f2d0e8df50000f90eed1681717e197afc2f73698f108bbb045eada6d3dd" {
		t.Fatal("legacy versions or hashes differ from new-api")
	}
	// The 10-question set built in up to 0.11.1.
	if RulesVersionZHv2 != "content-audit-v2-zh" || legacyWireHashZHv2 != "30e7aacadcba4eddab230c74955c966b3d5f80c610dc3f36f2ab5be2946ed748" {
		t.Fatal("the 0.11.1 built-in version or hash changed")
	}
	// The 9-question set before the task / material preamble.
	if RulesVersionZHv3 != "content-audit-v3-zh" || legacyWireHashZHv3 != "5fcd72bb76d0dc19aca0995066812eb55f0d36a9b6ab68023411208cdfcf9689" {
		t.Fatal("the v3 built-in version or hash changed")
	}
	if v := RulesVersionOf(&builtinQuestions); v != "content-audit-v4-zh" || RulesVersionZH != "content-audit-v4-zh" || len(builtinQuestions.Items) != 9 {
		t.Fatalf("built-in set = %s", v)
	}
	def, _ := DefaultQuestions(nil)
	if def.RulesVersion != RulesVersionZH || builtinVersions[jevWireHash(&def)] != RulesVersionZH {
		t.Fatalf("default set = %s", def.RulesVersion)
	}
	custom := def
	custom.Items = custom.Items[:1:1]
	if v := RulesVersionOf(&custom); v != jevCustomRulesPrefix+jevWireHash(&custom)[:8] {
		t.Fatalf("custom set = %s", v)
	}
}

// jevLargestSource is the most expensive image jevAsset redraws: a square of
// jevImageMaxSourcePixels (the scaler's buffer is largest for a square) in
// 16-bit RGBA, the widest pixel format a PNG decodes to.
func jevLargestSource(t testing.TB) string {
	side := 3464 // 3464² = 11,999,296
	if side*side > jevImageMaxSourcePixels || (side+1)*(side+1) <= jevImageMaxSourcePixels {
		t.Fatalf("side %d is not the largest square of %d pixels", side, jevImageMaxSourcePixels)
	}
	img := image.NewNRGBA64(image.Rect(0, 0, side, side))
	for i := 0; i < len(img.Pix); i += 8 {
		img.Pix[i], img.Pix[i+6], img.Pix[i+7] = 0xff, 0xff, 0xff
	}
	return encodePNG(t, img)
}

// jevNormalizePeak bounds what n redraws of raw started at once keep alive
// together, in bytes: the most one redraw keeps alive, times the redraws the
// slots and the decoder memory admit at once - the worst overlap, whatever
// the scheduling. The n redraws are then started together, and the test
// fails when more of them than that are ever in the decoder at once.
func jevNormalizePeak(t testing.TB, raw string, n int) uint64 {
	cfg, format, ok := imageHeader(raw)
	if !ok {
		t.Fatal("no image header")
	}
	cost := jevDecodeCost(format, cfg, base64.NewDecoder(base64.StdEncoding, strings.NewReader(raw)))
	admitted := min(n, cap(jevNormalizeSlots), int(jevNormalizeMemory/cost))
	one := jevRedrawHeld(t, raw, cfg)

	var inside, most atomic.Int64
	normalize := jevNormalize
	jevNormalize = func(data []byte) *typesafeAsset {
		in := inside.Add(1)
		defer inside.Add(-1)
		for m := most.Load(); in > m && !most.CompareAndSwap(m, in); m = most.Load() {
		}
		return normalize(data)
	}
	defer func() { jevNormalize = normalize }()
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if a := jevAsset(context.Background(), raw); a == nil {
				t.Error("the largest image was skipped")
			}
		})
	}
	wg.Wait()
	if m := most.Load(); m > int64(admitted) {
		t.Fatalf("%d redraws ran at once, the slots and the memory admit %d", m, admitted)
	}
	return uint64(admitted) * one
}

// jevRedrawHeld redraws raw alone and returns the most it holds at once, in
// bytes, from what the code allocates - not from the memory the process
// has, which also counts the garbage not collected yet and the pages not
// returned yet, and so depends on GC and scavenger timing and on the load
// of the machine. Everything the redraw allocates counts as held to its
// end, but for the scaler's buffer (scaleBuffers): each band of
// jevNormalizeImage allocates its own and drops it, so only the largest one
// is held at a time. The least of two redraws: a goroutine an earlier test
// left running can only add to the count.
func jevRedrawHeld(t testing.TB, raw string, cfg image.Config) uint64 {
	allocated := func() uint64 {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.TotalAlloc
	}
	all := uint64(math.MaxUint64)
	for range 2 {
		before := allocated()
		if jevAsset(context.Background(), raw) == nil {
			t.Fatal("the largest image was skipped")
		}
		all = min(all, allocated()-before)
	}
	total, largest := scaleBuffers(cfg.Width, cfg.Height)
	return all - total + largest
}

// scaleBuffers is the size of the buffers draw.BiLinear.Scale allocates
// when jevNormalizeImage scales a w×h image, in all and the largest: one
// [4]float64 per destination column and source row of each band.
func scaleBuffers(w, h int) (total, largest uint64) {
	long := max(w, h)
	if long <= jevImageMaxSide {
		return 0, 0
	}
	dw, dh := max(1, w*jevImageMaxSide/long), max(1, h*jevImageMaxSide/long)
	for y := 0; y < dh; y += jevScaleBandRows {
		y1 := min(y+jevScaleBandRows, dh)
		band := uint64(dw*(y1*h/dh-y*h/dh)) * 32
		total, largest = total+band, max(largest, band)
	}
	return total, largest
}

// jevNormalizeMemoryBudget is what redrawing may take of the memory (512 MB when this was sized, 1024 MB since 0.8.0) the
// plugin process is limited to (CONTRACTS §72.2, soft limit about 460 MiB);
// the rest is left for the request bodies and everything else the plugin
// holds.
const jevNormalizeMemoryBudget = 200 << 20

// Design Q10: the worst case of image normalization stays inside the memory
// limit of the plugin, with every slot busy. The worst case per pixel is a
// progressive JPEG without chroma subsampling (15 bytes per pixel in the
// decoder, from a file of a few KB), then a 16-bit PNG (8 bytes).
func TestJevNormalizeMemory(t *testing.T) {
	slots := cap(jevNormalizeSlots)
	check := func(name, raw string) {
		t.Helper()
		peak := jevNormalizePeak(t, raw, slots)
		t.Logf("memory of %d concurrent redraws of %s: at most %d MiB", slots, name, peak>>20)
		if peak > jevNormalizeMemoryBudget {
			t.Fatalf("%s: redrawing takes %d MiB, over the budget of %d MiB", name, peak>>20, jevNormalizeMemoryBudget>>20)
		}
	}
	check("a 12 MP 16-bit PNG", jevLargestSource(t))

	// The largest progressive 4:4:4 JPEG that is still redrawn.
	costOf := func(data []byte) int64 {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return jevDecodeCost("jpeg", cfg, bytes.NewReader(data))
	}
	side := 3464
	for costOf(progressiveJPEG(t, side, side)) > jevNormalizeMemory {
		side -= 8
	}
	if side*side < 7_000_000 {
		t.Fatalf("the largest progressive JPEG redrawn is only %d px square", side)
	}
	check(fmt.Sprintf("a %d×%d progressive 4:4:4 JPEG", side, side), b64(progressiveJPEG(t, side, side)))

	// Two images that fit the memory together run at once: 16-bit PNGs of
	// half the memory each.
	half := 2700 // 2700² × 8 bytes = 55.6 MiB
	if int64(half*half*8)*2 > jevNormalizeMemory {
		t.Fatalf("two %d px 16-bit squares do not fit %d MiB", half, jevNormalizeMemory>>20)
	}
	check(fmt.Sprintf("a %d×%d 16-bit PNG", half, half), encodePNG(t, image.NewNRGBA64(image.Rect(0, 0, half, half))))

	// The 12 MP progressive JPEG (180 MB to decode, a 70 KB file) is skipped
	// from its header, a baseline one of the same size is redrawn.
	big := progressiveJPEG(t, 3464, 3464)
	if len(big) > 100<<10 || costOf(big) < 170<<20 {
		t.Fatalf("12 MP progressive JPEG: %d bytes, cost %d MiB", len(big), costOf(big)>>20)
	}
	if a := jevAsset(context.Background(), b64(big)); a != nil {
		t.Fatal("a 12 MP progressive 4:4:4 JPEG was redrawn")
	}
	if len(jevNormalizeSlots) != 0 || !jevNormalizeBytes.TryAcquire(jevNormalizeMemory) {
		t.Fatal("slots or memory still taken")
	}
	jevNormalizeBytes.Release(jevNormalizeMemory)
}

// The decoder memory is estimated from the header alone.
func TestJevDecodeCost(t *testing.T) {
	cost := func(format string, data []byte) int64 {
		t.Helper()
		cfg, f, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || f != format {
			t.Fatalf("%s: %s %v", format, f, err)
		}
		return jevDecodeCost(format, cfg, bytes.NewReader(data))
	}
	raw := func(s string) []byte {
		data, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	const px = 1000 * 800
	within := func(name string, got, want int64) {
		t.Helper()
		if got < want || got > want+want/8 {
			t.Errorf("%s: cost %d, want %d (up to an eighth more)", name, got, want)
		}
	}
	within("8-bit PNG", cost("png", raw(encodePNG(t, filled(1000, 800, red)))), px*4)
	within("16-bit PNG", cost("png", raw(encodePNG(t, image.NewNRGBA64(image.Rect(0, 0, 1000, 800))))), px*8)
	within("gray PNG", cost("png", raw(encodePNG(t, image.NewGray(image.Rect(0, 0, 1000, 800))))), px)
	within("paletted PNG", cost("png", raw(encodePNG(t, image.NewPaletted(image.Rect(0, 0, 1000, 800), color.Palette{white, red})))), px)
	// The standard encoder writes baseline 4:2:0: a plane and two quarters.
	within("baseline 4:2:0 JPEG", cost("jpeg", raw(encodeJPEG(t, filled(1000, 800, red)))), px*3/2)
	within("gray JPEG", cost("jpeg", raw(encodeJPEG(t, image.NewGray(image.Rect(0, 0, 1000, 800))))), px)
	within("progressive 4:4:4 JPEG", cost("jpeg", progressiveJPEG(t, 1000, 800)), px*15)
	var g bytes.Buffer
	if err := gif.Encode(&g, filled(1000, 800, red), nil); err != nil {
		t.Fatal(err)
	}
	within("GIF", cost("gif", g.Bytes()), px)
	// A JPEG whose frame header cannot be found costs the worst case.
	if got := jpegDecodeCost(px, bytes.NewReader([]byte{0xff, 0xd8, 0xff, 0xda, 0, 2})); got < px*15 {
		t.Errorf("JPEG without a frame header: cost %d", got)
	}
}

func BenchmarkJevNormalizeLargest(b *testing.B) {
	raw := jevLargestSource(b)
	var peak uint64
	for b.Loop() {
		peak = max(peak, jevNormalizePeak(b, raw, cap(jevNormalizeSlots)))
	}
	b.ReportMetric(float64(peak)/(1<<20), "peak-MiB")
}

// progressiveJPEG is a w×h progressive JPEG (SOF2) without chroma
// subsampling, written by hand (the standard library only encodes baseline):
// one DC scan over the three components in which every block codes a zero
// difference with a one-bit Huffman code, so the file is tiny (one bit per
// block) while the decoder still allocates the coefficients of every block
// and the full YCbCr planes. It is the most expensive image to decode per
// source pixel and per file byte.
func progressiveJPEG(t testing.TB, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	seg := func(marker byte, payload []byte) {
		b.Write([]byte{0xff, marker})
		_ = binary.Write(&b, binary.BigEndian, uint16(len(payload)+2))
		b.Write(payload)
	}
	b.Write([]byte{0xff, 0xd8}) // SOI
	dqt := make([]byte, 65)     // table 0, every factor 1
	for i := 1; i < len(dqt); i++ {
		dqt[i] = 1
	}
	seg(0xdb, dqt)
	seg(0xc2, []byte{8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), 3,
		1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0}) // SOF2: 8 bit, three components at 1×1
	dht := make([]byte, 18) // DC table 0: one code of one bit for the value 0
	dht[1], dht[17] = 1, 0
	seg(0xc4, dht)
	seg(0xda, []byte{3, 1, 0, 2, 0, 3, 0, 0, 0, 0}) // SOS: DC of all three, first pass
	blocks := ((w + 7) / 8) * ((h + 7) / 8) * 3
	b.Write(make([]byte, (blocks+7)/8+2))
	b.Write([]byte{0xff, 0xd9}) // EOI
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b.Bytes()))
	if err != nil || format != "jpeg" || cfg.Width != w || cfg.Height != h {
		t.Fatalf("progressive JPEG header = %+v %s %v", cfg, format, err)
	}
	return b.Bytes()
}

// setRedraw replaces the redraw of an image until the test ends. The end of
// the test waits for the redraws still going on (a call that did not wait
// for its redraw leaves it behind, holding its slot) before the real redraw
// is put back, so no redraw reads the variable while it changes.
func setRedraw(t *testing.T, redraw func(data []byte) *typesafeAsset) {
	t.Helper()
	jevNormalize = redraw
	t.Cleanup(func() {
		waitFor(t, "the redraws left behind", func() bool { return len(jevNormalizeSlots) == 0 })
		jevNormalize = jevNormalizeImage
	})
}

// heldRedraws makes the redraws wait: the first free ones go through, every
// later one waits until release is called (at the end of the test at the
// latest). started gets a value for each redraw that starts, count is their
// number.
func heldRedraws(t *testing.T, free int64) (started <-chan struct{}, count *atomic.Int64, release func()) {
	t.Helper()
	begun := make(chan struct{}, 256)
	gate := make(chan struct{})
	release = sync.OnceFunc(func() { close(gate) })
	count = new(atomic.Int64)
	setRedraw(t, func(data []byte) *typesafeAsset {
		n := count.Add(1)
		begun <- struct{}{}
		if n > free {
			<-gate
		}
		return jevNormalizeImage(data)
	})
	t.Cleanup(release) // registered last, so it runs before setRedraw waits for the redraws
	return begun, count, release
}

// within fails the test when ch has nothing to give in time (a guard against
// hanging, not a measurement).
func within[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

// Images that need a slow redraw never cost the text its audit: redrawing
// has a share of the channel's time, the images that are not ready by then
// are skipped and the call goes out with the text and the ones that are.
func TestJevRedrawDoesNotStarveText(t *testing.T) {
	mod := newFakeMod(t)
	var bodies []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		req, _ := http.NewRequest(r.Method, mod.srv.URL+r.URL.Path, bytes.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer srv.Close()
	def, _ := DefaultQuestions(nil)
	a, err := compileAuditor(1, "j", AuditorTypeSafe, 1, AuditorConfig{}, nil, &def)
	if err != nil {
		t.Fatal(err)
	}
	category := def.Items[0].Category
	ch := &liveChannel{Type: ChannelTypeSafe, BaseURL: srv.URL, Key: "k", Model: "jev"}
	// The first redraw is ready at once; the second is not ready before the
	// test lets it go, which is after the call came back.
	_, redraws, _ := heldRedraws(t, 1)
	in := &Input{Text: "MOD-HIT-" + category + " violating text"}
	for i := range 8 { // different pictures, each too small to send as it is
		in.Images = append(in.Images, Image{Data: encodePNG(t, filled(100, 100, color.RGBA{uint8(i * 30), 0, 0, 0xff}))})
	}
	// The channel has 2 s, the redraws half of it. Without that share the
	// call would wait for the second redraw until its time is over and end
	// in a timeout before anything is sent.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := typesafeAudit(ctx, ch, a.questions, true, in)
	if err != nil || res.Calls != 1 || len(bodies) != 1 {
		t.Fatalf("result = %+v, %v (%d upstream calls)", res, err, len(bodies))
	}
	if res.Scores[category] < 0.9 || !strings.Contains(bodies[0], "violating text") {
		t.Fatalf("the text was not judged: scores = %v", res.Scores)
	}
	// The first image went out, the others were skipped; the result names
	// the one the upstream received.
	if res.SentImages != 1 || res.SkippedImages != 7 || !slices.Equal(res.received, []int{0}) {
		t.Fatalf("sent %d, skipped %d of %d images, received %v", res.SentImages, res.SkippedImages, len(in.Images), res.received)
	}
	if n := redraws.Load(); n != 2 {
		t.Fatalf("%d redraws started, want 2 (none after the budget ended)", n)
	}

	// Images sent as they are need no redraw and are packed whatever the
	// budget: here behind eight that are not ready in time.
	inLimit := encodePNG(t, filled(300, 300, blue))
	in.Images = append(in.Images, Image{Data: inLimit})
	short, cancelShort := context.WithTimeout(context.Background(), time.Second)
	defer cancelShort()
	if as := jevAssets(short, in); len(as) != 1 || as[0].DataBase64 != inLimit || !slices.Equal(as[0].from, []int{8}) {
		t.Fatalf("assets = %d, want the in-limits image only", len(as))
	}
}

// A context that is done redraws nothing more and returns at once, also
// while a redraw is running or the slots are free.
func TestJevRedrawStopsWithContext(t *testing.T) {
	started, redraws, release := heldRedraws(t, 0)
	in := &Input{Text: "t"}
	for i := range 8 {
		in.Images = append(in.Images, Image{Data: encodePNG(t, filled(100, 100, color.RGBA{0, uint8(i * 30), 0, 0xff}))})
	}
	// Done before the call: the slots are free, and still nothing starts
	// (a select between a free slot and a done context takes either).
	done, cancel := context.WithCancel(context.Background())
	cancel()
	for range 50 {
		if as := jevAssets(done, in); len(as) != 0 {
			t.Fatalf("%d assets with a canceled context", len(as))
		}
	}
	if redraws.Load() != 0 || len(jevNormalizeSlots) != 0 {
		t.Fatalf("%d redraws started with a canceled context", redraws.Load())
	}
	// Canceled during the first redraw: the call returns without waiting
	// for it (it is still held) and starts no other.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan []typesafeAsset, 1)
	go func() { result <- jevAssets(ctx, in) }()
	within(t, "the first redraw", started)
	cancel()
	if as := within(t, "the call after its context ended", result); len(as) != 0 {
		t.Fatalf("%d assets after the cancel", len(as))
	}
	if n := redraws.Load(); n != 1 {
		t.Fatalf("%d redraws started, want 1", n)
	}
	// The redraw left behind frees its slot and its memory when it is done.
	if len(jevNormalizeSlots) != 1 {
		t.Fatalf("%d slots taken while the redraw is held", len(jevNormalizeSlots))
	}
	release()
	waitFor(t, "the abandoned redraw", func() bool { return len(jevNormalizeSlots) == 0 })
	if !jevNormalizeBytes.TryAcquire(jevNormalizeMemory) {
		t.Fatal("memory of the abandoned redraw was not released")
	}
	jevNormalizeBytes.Release(jevNormalizeMemory)
	// Without a deadline the budget is the fixed maximum.
	if got := jevNormalizeBudget(context.Background()); got != jevNormalizeBudgetMax {
		t.Fatalf("budget without a deadline = %s", got)
	}
	long, cancelLong := context.WithTimeout(context.Background(), time.Minute)
	defer cancelLong()
	if got := jevNormalizeBudget(long); got != jevNormalizeBudgetMax {
		t.Fatalf("budget of a long call = %s", got)
	}
}

// A redraw waits for a slot and for the decoder memory only while its
// context lasts: when it ends during the wait, the image is skipped, nothing
// is decoded and nothing is kept.
func TestJevRedrawGivesUpWaiting(t *testing.T) {
	_, redraws, _ := heldRedraws(t, 1<<30) // nothing is held
	small := encodePNG(t, filled(100, 100, red))
	const cost = 100 * 100 * 4
	redraw := func(ctx context.Context) <-chan *typesafeAsset {
		out := make(chan *typesafeAsset, 1)
		go func() { out <- jevRedraw(ctx, small, cost) }()
		return out
	}
	waiting := func(what string, out <-chan *typesafeAsset) {
		t.Helper()
		select {
		case a := <-out:
			t.Fatalf("the redraw did not wait for %s (asset: %v)", what, a != nil)
		case <-time.After(100 * time.Millisecond): // it waits; a longer look would say no more
		}
	}

	// Both slots are taken.
	freeSlots := busySlots(t)
	defer freeSlots()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := redraw(ctx)
	waiting("a slot", out)
	cancel()
	if a := within(t, "the redraw that waited for a slot", out); a != nil {
		t.Fatal("an image was redrawn without a slot")
	}
	if len(jevNormalizeSlots) != cap(jevNormalizeSlots) || redraws.Load() != 0 {
		t.Fatalf("after the wait for a slot: %d slots taken, %d redraws", len(jevNormalizeSlots), redraws.Load())
	}
	freeSlots()

	// The slots are free, the decoder memory is taken: the redraw holds a
	// slot while it waits, and gives it back.
	if !jevNormalizeBytes.TryAcquire(jevNormalizeMemory) {
		t.Fatal("decoder memory in use")
	}
	freeMemory := sync.OnceFunc(func() { jevNormalizeBytes.Release(jevNormalizeMemory) })
	defer freeMemory()
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	out = redraw(ctx)
	waitFor(t, "the redraw to take a slot", func() bool { return len(jevNormalizeSlots) == 1 })
	waiting("the memory", out)
	cancel()
	if a := within(t, "the redraw that waited for memory", out); a != nil {
		t.Fatal("an image was redrawn without its memory")
	}
	if len(jevNormalizeSlots) != 0 || redraws.Load() != 0 {
		t.Fatalf("after the wait for memory: %d slots taken, %d redraws", len(jevNormalizeSlots), redraws.Load())
	}
	// Nothing of the memory was taken or given back by the redraw that gave up.
	freeMemory()
	if !jevNormalizeBytes.TryAcquire(jevNormalizeMemory) {
		t.Fatal("the redraw that gave up kept decoder memory")
	}
	jevNormalizeBytes.Release(jevNormalizeMemory)
	// With both free the same image is redrawn.
	if a := jevRedraw(context.Background(), small, cost); a == nil || redraws.Load() != 1 {
		t.Fatalf("redraw with free slots: %v, %d redraws", a != nil, redraws.Load())
	}
}

// A decoder that panics on an image costs that image only: it is skipped,
// counted and logged without its content, and its slot and memory are free
// again.
func TestJevRedrawDecoderPanic(t *testing.T) {
	setRedraw(t, func([]byte) *typesafeAsset { panic("decoder bug") })
	var logged bytes.Buffer
	old := jevLogger.Swap(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { jevLogger.Store(old) })
	small := encodePNG(t, filled(100, 100, red))
	ok := encodePNG(t, filled(300, 300, red))
	before := jevRedrawPanics.Load()
	as := jevAssets(context.Background(), &Input{Text: "t", Images: []Image{{Data: small}, {Data: ok}}})
	if len(as) != 1 || as[0].DataBase64 != ok || !slices.Equal(as[0].from, []int{1}) {
		t.Fatalf("assets = %d, want the image that needs no redraw", len(as))
	}
	if n := jevRedrawPanics.Load() - before; n != 1 {
		t.Fatalf("decoder panics counted = %d", n)
	}
	if len(jevNormalizeSlots) != 0 {
		t.Fatalf("%d slots still taken", len(jevNormalizeSlots))
	}
	if !jevNormalizeBytes.TryAcquire(jevNormalizeMemory) {
		t.Fatal("memory of the panicked redraw was not released")
	}
	jevNormalizeBytes.Release(jevNormalizeMemory)
	line := logged.String()
	if !strings.Contains(line, "level=ERROR") || !strings.Contains(line, "image decoder panicked") || !strings.Contains(line, "decoder bug") ||
		!strings.Contains(line, fmt.Sprintf("base64_bytes=%d", len(small))) {
		t.Fatalf("log = %q", line)
	}
	if strings.Contains(line, small[:40]) {
		t.Fatal("the log holds the image")
	}
}
