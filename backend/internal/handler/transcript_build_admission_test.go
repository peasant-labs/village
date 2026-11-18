package handler

import (
	"context"
	"errors"
	"testing"
)

func TestHeavyWorkAdmissionAccounting(t *testing.T) {
	c := newTranscriptContentCache(transcriptContentCacheConfig{Budget: 100})
	first, err := c.AcquireBuild(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.AcquireBuild(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AcquireBuild(context.Background()); !errors.Is(err, errTranscriptReadBusy) {
		t.Fatalf("third build: %v", err)
	}
	if c.heavyWork != 2 || c.reserved != 0 {
		t.Fatalf("refusal changed accounting: %d, %d", c.heavyWork, c.reserved)
	}
	first()
	second()
	if c.heavyWork != 0 {
		t.Fatalf("slots leaked: %d", c.heavyWork)
	}
}
