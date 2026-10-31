package handler

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/authoritative_projection.yaml
var authoritativeProjectionYAML []byte

// TestProjectAuthoritativePublishKeepsEveryLeaf fills every field with a
// distinct value, so a re-pin that renames a wire key on one side, or a field
// the projection stops carrying, fails here instead of persisting an empty
// column.
func TestProjectAuthoritativePublishKeepsEveryLeaf(t *testing.T) {
	var fixture struct {
		Renamed      map[string]string `yaml:"renamed"`
		NotProjected []string          `yaml:"not_projected"`
	}
	if err := yaml.Unmarshal(authoritativeProjectionYAML, &fixture); err != nil {
		t.Fatal(err)
	}
	var authoritative schema.AuthoritativePublishRequest
	next := 0
	populate(reflect.ValueOf(&authoritative).Elem(), &next, 0)

	req, stored, err := projectAuthoritativePublish(authoritative)
	if err != nil {
		t.Fatal(err)
	}
	projectedBytes, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	source, projected := jsonLeaves(t, stored), jsonLeaves(t, projectedBytes)

	var lost []string
	for path, value := range source {
		target := path
		if renamed, ok := fixture.Renamed[path]; ok {
			target = renamed
		}
		if got, ok := projected[target]; !ok || got != value {
			lost = append(lost, path)
		}
	}
	for path := range fixture.Renamed {
		if _, ok := source[path]; !ok {
			t.Errorf("fixture renames %s, which the authoritative request no longer carries", path)
		}
	}
	want := append([]string(nil), fixture.NotProjected...)
	sort.Strings(lost)
	sort.Strings(want)
	if !reflect.DeepEqual(lost, want) {
		t.Fatalf("fields missing from the legacy projection\n got: %v\nwant: %v", lost, want)
	}
}

func populate(v reflect.Value, next *int, depth int) {
	if depth > 6 {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		populate(v.Elem(), next, depth+1)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				populate(v.Field(i), next, depth+1)
			}
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		populate(v.Index(0), next, depth+1)
	case reflect.Map:
		key := reflect.New(v.Type().Key()).Elem()
		populate(key, next, depth+1)
		value := reflect.New(v.Type().Elem()).Elem()
		populate(value, next, depth+1)
		v.Set(reflect.MakeMap(v.Type()))
		v.SetMapIndex(key, value)
	case reflect.String:
		*next++
		v.SetString(fmt.Sprintf("value-%d", *next))
	case reflect.Int, reflect.Int32, reflect.Int64:
		*next++
		v.SetInt(int64(*next))
	case reflect.Float64:
		*next++
		v.SetFloat(float64(*next))
	case reflect.Bool:
		v.SetBool(true)
	}
}

func jsonLeaves(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	leaves := map[string]any{}
	var walk func(string, any)
	walk = func(path string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				walk(path+"."+key, child)
			}
		case []any:
			for _, child := range typed {
				walk(path+"[]", child)
			}
		default:
			leaves[path] = value
		}
	}
	walk("", document)
	return leaves
}
