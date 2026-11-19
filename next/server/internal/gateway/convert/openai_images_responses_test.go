package convert

import (
	"context"
	"errors"
	"testing"
)

// The image pairs are same-platform pairs of their own: never composed into
// a pair to another platform, and they place the client's model in the
// tool (CONTRACTS §121).
func TestImagesPairs(t *testing.T) {
	reg := Default()
	for _, from := range []string{protocolImages, protocolImagesEdit} {
		c, ok := reg.Resolve(from, relay)
		if !ok || c.Grade() != GradeGood {
			t.Fatalf("%s -> %s: %v", from, relay, ok)
		}
		if ml, ok := c.(ModelLocator); !ok || ml.ModelPath() != "tools.0.model" {
			t.Fatalf("%s: model path", from)
		}
		for _, p := range reg.Pairs() {
			if p.From == from && p.To != relay {
				t.Errorf("%s composed to %s", from, p.To)
			}
		}
	}
}

// Without the adapted model the converter cannot build the request: a
// configuration error, not the request's.
func TestImagesNeedAdaptModel(t *testing.T) {
	c, _ := Default().Resolve(protocolImages, relay)
	_, _, err := c.Prepare([]byte(`{"model":"gpt-image-2","prompt":"x"}`), Options{Ctx: context.Background()})
	if err == nil || errors.As(err, new(*RequestError)) {
		t.Fatalf("err = %v", err)
	}
	bound, _, err := c.Prepare([]byte(`{"model":"gpt-image-2","prompt":"x"}`), Options{Ctx: context.Background(), AdaptModel: "gpt-6-luna"})
	if err != nil {
		t.Fatal(err)
	}
	// A stream that ends without its terminal event is a conversion failure.
	s := bound.NewStream()
	if _, err := s.Event(Event{Data: []byte(`{"type":"response.created","response":{"created_at":1}}`)}); err != nil {
		t.Fatal(err)
	}
	if evs, err := s.Flush(); err == nil || len(evs) > 0 {
		t.Fatalf("flush = %v, %v", evs, err)
	}
	if _, err := c.Response([]byte(`{}`)); !errors.Is(err, errUnprepared) {
		t.Fatalf("unbound response: %v", err)
	}
}
