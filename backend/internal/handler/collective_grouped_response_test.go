package handler

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/collective_grouped_response.yaml
var collectiveResponseFixtureBytes []byte

type collectiveResponseCase struct {
	Name          string                    `yaml:"name"`
	Route         string                    `yaml:"route"`
	InputCount    *int64                    `yaml:"input_count"`
	Status        schema.VillageShareStatus `yaml:"status"`
	AlreadyShared bool                      `yaml:"already_shared"`
}

type collectiveResponseFixtures struct {
	Session       string                   `yaml:"session"`
	OwnerID       string                   `yaml:"owner_id"`
	OwnerUsername string                   `yaml:"owner_username"`
	OwnerAvatar   string                   `yaml:"owner_avatar"`
	SharedAt      time.Time                `yaml:"shared_at"`
	Cases         []collectiveResponseCase `yaml:"cases"`
}

func loadCollectiveResponseFixtures(t *testing.T) collectiveResponseFixtures {
	t.Helper()
	d := yaml.NewDecoder(bytes.NewReader(collectiveResponseFixtureBytes))
	d.KnownFields(true)
	var f collectiveResponseFixtures
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("expected one fixture document, got %v", err)
	}
	required := map[string]bool{
		"collective-positive-count": false, "collective-unknown-count": false,
		"pending-measured-zero": false, "my-shares-positive-count": false,
		"contributable-positive-count": false,
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("empty or duplicate case %q", c.Name)
		}
		seen[c.Name] = true
		if _, ok := required[c.Name]; ok {
			required[c.Name] = true
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("missing required case %s", name)
		}
	}
	return f
}

func TestCollectiveGroupedResponsePreservesRowFields(t *testing.T) {
	f := loadCollectiveResponseFixtures(t)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var session schema.VillageTranscript
			if err := json.Unmarshal([]byte(f.Session), &session); err != nil {
				t.Fatal(err)
			}
			session.InputSubmissionCount = c.InputCount
			owner := schema.NewVillagePublicProfile(schema.VillageUUID(f.OwnerID), f.OwnerUsername, nil, &f.OwnerAvatar)
			var row schema.VillageSessionRow
			var arm any
			switch c.Route {
			case "collective":
				row = collectiveSessionRow(session, owner)
				arm = row.Collective
				if !reflect.DeepEqual(row.Collective.Owner, owner) || !reflect.DeepEqual(row.Owner, owner) {
					t.Fatal("owner metadata lost")
				}
			case "pending":
				row = pendingSessionRow(session, owner, f.SharedAt)
				arm = row.Pending
				if !reflect.DeepEqual(row.Pending.Owner, owner) || !reflect.DeepEqual(row.Owner, owner) || !row.Pending.SharedAt.Equal(f.SharedAt) {
					t.Fatal("pending review metadata lost")
				}
			case "my-shares":
				row = myShareSessionRow(session, owner, c.Status, f.SharedAt)
				arm = row.MyShare
				if row.MyShare.Status != c.Status || !row.MyShare.SharedAt.Equal(f.SharedAt) || !reflect.DeepEqual(row.MyShare.Owner, owner) {
					t.Fatal("submission state lost")
				}
			case "contributable":
				row = contributableSessionRow(session, owner, c.AlreadyShared)
				arm = row.Contributable
				if row.Contributable.AlreadyShared != c.AlreadyShared {
					t.Fatal("eligibility lost")
				}
			default:
				t.Fatalf("unregistered fixture route %q", c.Route)
			}
			if err := row.Validate(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(row.Session, session) {
				t.Fatal("common session altered")
			}
			// Check EVERY shared Go field, not just the subset validated by the
			// contract. Adding a summary field cannot silently lose its value.
			sv, av := reflect.ValueOf(session), reflect.ValueOf(arm).Elem()
			for i := 0; i < av.NumField(); i++ {
				name := av.Type().Field(i).Name
				if name == "TranscriptID" {
					name = "ID"
				}
				if name == "Branch" {
					name = "GitBranch"
				}
				common := sv.FieldByName(name)
				// The session's repository fields are nullable (withheld for an
				// anonymous owner); an arm that keeps them required carries
				// the same value.
				if common.IsValid() && common.Kind() == reflect.Pointer && av.Field(i).Kind() != reflect.Pointer && !common.IsNil() {
					common = common.Elem()
				}
				if common.IsValid() && !reflect.DeepEqual(common.Interface(), av.Field(i).Interface()) {
					t.Errorf("route field %s differs from session", name)
				}
			}
			encoded, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			var roundTrip schema.VillageSessionRow
			if err := json.Unmarshal(encoded, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roundTrip, row) {
				t.Fatal("wire round trip lost row data")
			}
		})
	}
}
