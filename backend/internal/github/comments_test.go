package github

import (
	"context"
	"strings"
	"testing"
)

func TestUpsertIssueComment_CreatesTheFirstComment(t *testing.T) {
	a := &appServer{}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	comment, err := c.UpsertIssueComment(context.Background(), 42, "acme", "repo", 7, 0, "3 prompts attached")
	if err != nil {
		t.Fatalf("UpsertIssueComment: %v", err)
	}
	if comment.ID != 555 {
		t.Errorf("comment id = %d, want the id GitHub returned", comment.ID)
	}

	req := a.lastRequest()
	if req.Method != "POST" || req.Path != "/repos/acme/repo/issues/7/comments" {
		t.Fatalf("request = %s %s, want POST /repos/acme/repo/issues/7/comments", req.Method, req.Path)
	}
	if got := decodeObject(t, req.Body)["body"]; got != "3 prompts attached" {
		t.Errorf("body = %v, want the comment body", got)
	}
}

// TestUpsertIssueComment_EditsTheRecordedComment is the sticky-comment rule: a
// recorded comment id must edit that comment, and must never post a second one
// for a later push.
func TestUpsertIssueComment_EditsTheRecordedComment(t *testing.T) {
	a := &appServer{}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	if _, err := c.UpsertIssueComment(context.Background(), 42, "acme", "repo", 7, 555, "4 prompts attached"); err != nil {
		t.Fatalf("UpsertIssueComment: %v", err)
	}

	if len(a.requests) != 1 {
		t.Fatalf("made %d requests, want exactly one edit and no create", len(a.requests))
	}
	req := a.requests[0]
	if req.Method != "PATCH" || req.Path != "/repos/acme/repo/issues/comments/555" {
		t.Fatalf("request = %s %s, want PATCH /repos/acme/repo/issues/comments/555", req.Method, req.Path)
	}
	if got := decodeObject(t, req.Body)["body"]; got != "4 prompts attached" {
		t.Errorf("body = %v, want the new comment body", got)
	}
}

func TestUpsertIssueComment_RequiresANumberWhenCreating(t *testing.T) {
	a := &appServer{}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	if _, err := c.UpsertIssueComment(context.Background(), 42, "acme", "repo", 0, 0, "body"); err == nil {
		t.Fatal("expected an error when creating with no pull request number")
	}
	if len(a.requests) != 0 {
		t.Errorf("a refused create still called GitHub: %+v", a.requests)
	}
}

func TestUpdateIssueComment_RequiresAnID(t *testing.T) {
	a := &appServer{}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	if _, err := c.UpdateIssueComment(context.Background(), 42, "acme", "repo", 0, "body"); err == nil {
		t.Fatal("expected an error for a non-positive comment id")
	}
	if len(a.requests) != 0 {
		t.Errorf("a refused update still called GitHub: %+v", a.requests)
	}
}

func TestDeleteIssueComment_DeletesTheRecordedComment(t *testing.T) {
	a := &appServer{}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	if err := c.DeleteIssueComment(context.Background(), 42, "acme", "repo", 555); err != nil {
		t.Fatalf("DeleteIssueComment: %v", err)
	}

	req := a.lastRequest()
	if req.Method != "DELETE" || req.Path != "/repos/acme/repo/issues/comments/555" {
		t.Fatalf("request = %s %s, want DELETE /repos/acme/repo/issues/comments/555", req.Method, req.Path)
	}
}

func TestDeleteIssueComment_Non2xxSurfacesError(t *testing.T) {
	a := &appServer{failWrite: true}
	newAppServer(t, a)
	c := newTestClient(t, a.srv.URL)

	err := c.DeleteIssueComment(context.Background(), 42, "acme", "repo", 555)
	if err == nil {
		t.Fatal("expected a non-2xx response to surface as an error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to name the status", err)
	}
	if len(a.requests) != 1 {
		t.Errorf("made %d requests, want exactly 1: the client never retries", len(a.requests))
	}
}
