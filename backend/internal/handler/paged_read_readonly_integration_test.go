//go:build integration

package handler

import (
	"bytes"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

// This test needs the real publication, read-repair and encrypted object paths:
// a unit test cannot establish that neither the row nor bucket changed.
// It owns its account and generations and uses the normal audited cleanup.
// The bucket snapshot observes external effects; the test must run without
// concurrent writers to the test bucket. The final content read proves that
// the fixture actually triggers repair and that the two bodies agree.
func TestPagedReadCheckLegacyDoesNotWrite(t *testing.T) {
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	w := newOrderingWorld(t, pool, objects, 68001, legacyBarePayloadJSON("claude"))
	client := s3.New(s3.Options{
		Region: "us-east-1", BaseEndpoint: aws.String(os.Getenv("TEST_S3_ENDPOINT")),
		Credentials:  credentials.NewStaticCredentialsProvider(os.Getenv("TEST_S3_ACCESS_KEY"), os.Getenv("TEST_S3_SECRET_KEY"), ""),
		UsePathStyle: true,
	})
	list := func() []string {
		t.Helper()
		paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(os.Getenv("TEST_S3_BUCKET"))})
		var keys []string
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(w.ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, object := range page.Contents {
				keys = append(keys, aws.ToString(object.Key))
			}
		}
		sort.Strings(keys)
		return keys
	}
	before := w.row()
	beforeKeys := list()
	var report bytes.Buffer
	err := w.handler.RunPagedReadCheck(w.ctx, &report, PagedReadCheckOptions{
		Mode: PagedReadCheckTranscripts, Transcripts: []uuid.UUID{uuidFromPg(w.id)}, AuthorizationRef: "test/legacy",
	})
	if err != nil {
		t.Fatalf("check: %v; report: %s", err, report.String())
	}
	observation, err := w.handler.observePagedReads(w.ctx, w.handler.pagedReadRoutes(), uuidFromPg(w.id), w.owner)
	if err != nil || observation.ContentStatus != 200 {
		t.Fatalf("observe: status=%d err=%v", observation.ContentStatus, err)
	}
	after := w.row()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("the check changed the stored row, including revision, identity or updated_at")
	}
	if afterKeys := list(); !reflect.DeepEqual(beforeKeys, afterKeys) {
		t.Fatalf("the check changed the object listing: before=%v after=%v", beforeKeys, afterKeys)
	}
	if _, writes := w.store.counts(); writes != 0 {
		t.Fatalf("the check wrote %d object generations", writes)
	}
	response := getContent(t, w.handler, uuidFromPg(w.id))
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), observation.Content) {
		t.Fatalf("the check and content route differ: status=%d", response.Code)
	}
	if _, writes := w.store.counts(); writes == 0 {
		t.Fatal("the legacy content route did not rewrite; this fixture does not exercise read repair")
	}
}
