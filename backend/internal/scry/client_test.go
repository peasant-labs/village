package scry

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/scry-client.yaml
var scryClientYAML []byte

type clientCase struct {
	Name       string `yaml:"name"`
	Op         string `yaml:"op"`
	Turns      int    `yaml:"turns"`
	DeadlineMs int    `yaml:"deadline_ms"`
	Server     struct {
		Status     int    `yaml:"status"`
		Body       string `yaml:"body"`
		PadBytes   int    `yaml:"pad_bytes"`
		DelayMs    int    `yaml:"delay_ms"`
		RedirectTo string `yaml:"redirect_to"`
	} `yaml:"server"`
	Expect struct {
		Class        string `yaml:"class"`
		Verdict      string `yaml:"verdict"`
		Flagged      []int  `yaml:"flagged"`
		ScryVersion  string `yaml:"scry_version"`
		BearerSeen   bool   `yaml:"bearer_seen"`
		RedirectHits *int   `yaml:"redirect_target_hits"`
	} `yaml:"expect"`
}

var requiredClientCases = []string{
	"checked_empty_round_trips", "checked_with_turns_round_trips", "unchecked_round_trips",
	"bearer_header_sent", "redirect_not_followed", "turn_out_of_range_invalid",
	"unchecked_with_turns_invalid", "unknown_verdict_invalid", "health_contract_mismatch_rejected",
	"health_reports_version", "html_200_invalid", "status_413_too_large", "status_401_rejected",
	"status_503_unavailable", "deadline_is_timeout", "oversized_response_invalid",
	"error_text_has_no_secret_or_body",
}

const testSecret = "0123456789abcdef0123456789abcdef-secret"

func loadClientCases(t *testing.T) []clientCase {
	t.Helper()
	var cases []clientCase
	dec := yaml.NewDecoder(bytes.NewReader(scryClientYAML))
	dec.KnownFields(true)
	if err := dec.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		present[c.Name] = true
	}
	for _, name := range requiredClientCases {
		if !present[name] {
			t.Errorf("fixture is missing required case %q", name)
		}
	}
	return cases
}

func testRequest(turns int) schema.ScryCheckRequest {
	return schema.ScryCheckRequest{
		ContractVersion: schema.ScryContractVersion,
		TranscriptID:    schema.TranscriptID("11111111-1111-4111-8111-111111111111"),
		SessionDetail:   schema.SessionDetailPayload{ID: "s", Turns: make([]schema.TurnDetail, turns)},
		Record:          schema.ScryTranscriptRecord{PublishedAtMs: 1, UpdatedAtMs: 2},
	}
}

func TestScryClient(t *testing.T) {
	for _, c := range loadClientCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			var bearerOK, redirectHits atomic.Int32
			var sawRequestTurns atomic.Int32
			sawRequestTurns.Store(-1)
			mux := http.NewServeMux()
			serve := func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "Bearer "+testSecret {
					bearerOK.Add(1)
				}
				if r.URL.Path == "/v1/check" {
					raw, _ := io.ReadAll(r.Body)
					var shape struct {
						SessionDetail struct {
							Turns []json.RawMessage `json:"turns"`
						} `json:"sessionDetail"`
					}
					if err := json.Unmarshal(raw, &shape); err == nil {
						sawRequestTurns.Store(int32(len(shape.SessionDetail.Turns)))
					}
				}
				if c.Server.DelayMs > 0 {
					select {
					case <-time.After(time.Duration(c.Server.DelayMs) * time.Millisecond):
					case <-r.Context().Done():
						return
					}
				}
				if c.Server.RedirectTo != "" {
					http.Redirect(w, r, c.Server.RedirectTo, c.Server.Status)
					return
				}
				body := strings.ReplaceAll(c.Server.Body, "{contract}", schema.ScryContractVersion)
				body = strings.ReplaceAll(body, "{secret}", testSecret)
				status := c.Server.Status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				io.WriteString(w, body)
				io.WriteString(w, strings.Repeat(" ", c.Server.PadBytes))
			}
			mux.HandleFunc("/v1/check", serve)
			mux.HandleFunc("/healthz", serve)
			mux.HandleFunc("/elsewhere", func(w http.ResponseWriter, r *http.Request) {
				redirectHits.Add(1)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			client := NewHTTPClient(Settings{URL: srv.URL, SharedSecret: testSecret})
			ctx := context.Background()
			if c.DeadlineMs > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Duration(c.DeadlineMs)*time.Millisecond)
				defer cancel()
			}

			var err error
			var checkResp schema.ScryCheckResponse
			var healthResp schema.ScryHealthResponse
			if c.Op == "health" {
				healthResp, err = client.Health(ctx)
			} else {
				checkResp, err = client.Check(ctx, testRequest(c.Turns))
			}

			if err != nil {
				text := err.Error()
				for _, banned := range []string{testSecret, srv.URL, strings.TrimPrefix(srv.URL, "http://"), "BODYMARKER"} {
					if strings.Contains(text, banned) {
						t.Fatalf("error text %q carries %q", text, banned)
					}
				}
			}
			if c.Expect.Class != "" {
				if err == nil {
					t.Fatalf("got no error, want class %s", c.Expect.Class)
				}
				if got := Classify(err); string(got) != c.Expect.Class {
					t.Fatalf("class = %s (%v), want %s", got, err, c.Expect.Class)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.Expect.RedirectHits != nil && int(redirectHits.Load()) != *c.Expect.RedirectHits {
				t.Fatalf("redirect target hit %d times, want %d", redirectHits.Load(), *c.Expect.RedirectHits)
			}
			if c.Expect.BearerSeen && bearerOK.Load() == 0 {
				t.Fatal("server never saw the bearer header")
			}
			if c.Expect.Class != "" {
				return
			}
			if c.Op == "health" {
				if healthResp.ScryVersion != c.Expect.ScryVersion {
					t.Fatalf("health version = %q", healthResp.ScryVersion)
				}
				return
			}
			if string(checkResp.Verdict) != c.Expect.Verdict || checkResp.ScryVersion != c.Expect.ScryVersion || !reflect.DeepEqual(append([]int{}, checkResp.FlaggedTurns...), append([]int{}, c.Expect.Flagged...)) {
				t.Fatalf("response = %+v, want %+v", checkResp, c.Expect)
			}
			if got := int(sawRequestTurns.Load()); got != c.Turns {
				t.Fatalf("server saw %d turns, want %d", got, c.Turns)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	if got := Classify(context.DeadlineExceeded); got != ClassTimeout {
		t.Fatalf("deadline = %s", got)
	}
	if got := Classify(fmt.Errorf("wrapped: %w", &Error{Class: ClassTooLarge, Status: 413})); got != ClassTooLarge {
		t.Fatalf("wrapped = %s", got)
	}
	if got := Classify(errors.New("anything else")); got != ClassUnavailable {
		t.Fatalf("other = %s", got)
	}
}

func TestSettings(t *testing.T) {
	long := strings.Repeat("a", 32)
	if !(Settings{}).Unset() || (Settings{URL: "x"}).Unset() {
		t.Fatal("Unset is wrong")
	}
	if p := (Settings{URL: "http://scry.internal:8080", SharedSecret: long}).Problems(); len(p) != 0 {
		t.Fatalf("valid settings have problems: %v", p)
	}
	for _, s := range []Settings{
		{URL: "ftp://scry", SharedSecret: long},
		{URL: "http://", SharedSecret: long},
		{URL: "scry", SharedSecret: long},
		{URL: "http://scry", SharedSecret: long[:31]},
	} {
		if len((s).Problems()) == 0 {
			t.Fatalf("%+v should have problems", s)
		}
	}
}

func TestFake(t *testing.T) {
	f := NewFake(t, "1.2.3")
	c := NewHTTPClient(f.Settings())
	h, err := c.Health(context.Background())
	if err != nil || h.ScryVersion != "1.2.3" {
		t.Fatalf("health = %+v, %v", h, err)
	}
	req := testRequest(2)
	got, err := c.Check(context.Background(), req)
	if err != nil || got.Verdict != schema.ScryVerdictChecked || len(got.FlaggedTurns) != 0 {
		t.Fatalf("default answer = %+v, %v", got, err)
	}
	f.Answer(HashOf(req.SessionDetail), schema.ScryCheckResponse{ScryVersion: "1.2.3", Verdict: schema.ScryVerdictChecked, FlaggedTurns: []int{1}})
	got, err = c.Check(context.Background(), req)
	if err != nil || !reflect.DeepEqual(got.FlaggedTurns, []int{1}) {
		t.Fatalf("answered = %+v, %v", got, err)
	}
	f.Fail(503)
	if _, err = c.Check(context.Background(), req); Classify(err) != ClassUnavailable {
		t.Fatalf("fail = %v", err)
	}
	f.Fail(0)
	if f.Unanswered() != 1 {
		t.Fatalf("unanswered = %d, want 1 (the default-answered request)", f.Unanswered())
	}
	f.Answer(HashOf(req.SessionDetail), schema.ScryCheckResponse{ScryVersion: "1.2.3", Verdict: schema.ScryVerdictChecked, FlaggedTurns: []int{0}})
	if _, err = c.Check(context.Background(), req); err != nil || f.Unanswered() != 1 {
		t.Fatalf("an answered request must not count: %v, %d", err, f.Unanswered())
	}
	if len(f.Requests()) != 3 {
		t.Fatalf("recorded %d requests", len(f.Requests()))
	}
}
