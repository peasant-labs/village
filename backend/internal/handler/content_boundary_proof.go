package handler

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/observed_model_preservation/legacy_dispatch.yaml
var legacyDispatchYAML []byte

type legacyDispatchCase struct {
	Name          string `yaml:"name"`
	Context       string `yaml:"context"`
	Content       string `yaml:"content"`
	PublishStatus int    `yaml:"publishStatus"`
	ReadStatus    int    `yaml:"readStatus"`
}

func loadLegacyDispatchFixtures() ([]legacyDispatchCase, error) {
	var corpus struct {
		Cases []legacyDispatchCase `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(legacyDispatchYAML))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		return nil, err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("content boundary proof fixture must contain exactly one YAML document; restore the corpus before advertising preservation")
	}
	seen := map[string]bool{}
	for _, c := range corpus.Cases {
		if c.Name == "" || seen[c.Name] || c.Content == "" {
			return nil, fmt.Errorf("content boundary proof fixture has empty or duplicate case %q; restore unique named cases before advertising preservation", c.Name)
		}
		seen[c.Name] = true
		if c.Context != "" && c.Context != "pi" {
			return nil, fmt.Errorf("content boundary proof fixture %q has unsupported context; restore the corpus before advertising preservation", c.Name)
		}
		if (c.PublishStatus != http.StatusCreated && c.PublishStatus != http.StatusConflict) || (c.ReadStatus != http.StatusOK && c.ReadStatus != http.StatusInternalServerError) {
			return nil, fmt.Errorf("content boundary proof fixture %q has unsupported expectations; restore the corpus before advertising preservation", c.Name)
		}
	}
	for _, name := range strings.Fields(`
		sparse_legacy_envelope sparse_legacy_bare opaque_plaintext legacy_provider_native_usage
		legacy_jsonl legacy_array empty_harness_canonical_rewrite older_assistant_observation
		historical_nonassistant_observation invalid_older_observation pi_context_opaque
		pi_context_sparse_nonpi pi_context_no_identity explicit_pi_sparse legacy_key_pi_not_normalized
		metadata_null_is_presence metadata_empty_is_presence usage_null_is_presence usage_empty_is_presence
		source_ref_empty_is_presence tool_usage_null_is_presence call_ref_empty_is_presence
		result_ref_null_is_presence namespace_empty_requires_release namespace_null_requires_release
		malformed_json_not_opaque duplicate_escaped_legacy_key malformed_interior_jsonl
		public_envelope_mixed_jsonl public_bare_mixed_array recursively_wrapped_public_envelope
		wrong_harness_metadata old_role_plus_new_marker complete_reusable_nonpi_usage
		complete_nonassistant_observation_with_new_evidence complete_nullable_usage
		missing_discriminator_does_not_hide_new_fields native_tool_input_reserved_keys
		provider_message_tool_arguments_opaque native_tool_result_reserved_keys
		standalone_tool_block_arguments_opaque ordinary_scalar_keys_are_not_public_roots
		opaque_data_still_rejects_duplicate_keys wrapper_named_input_is_not_a_tool_boundary
		provider_record_sibling_public_root_rejected native_metadata_key_inside_provider_is_opaque
		native_model_parts_tool_arguments_opaque native_developer_message_content_opaque
	`) {
		if !seen[name] {
			return nil, fmt.Errorf("content boundary proof fixture lacks required case %q; restore the corpus before advertising preservation", name)
		}
	}
	return corpus.Cases, nil
}

type contentBoundaryValidator func([]byte, string, contentBoundaryMode) (contentBoundary, error)

// The actual publication/read boundary must preserve both legacy eligibility
// and refusal of canonical-invalid evidence before capabilities are advertised.
func proveContentBoundary(validator contentBoundaryValidator) error {
	cases, err := loadLegacyDispatchFixtures()
	if err != nil {
		return err
	}
	for _, c := range cases {
		_, publishErr := validator([]byte(c.Content), c.Context, contentPublication)
		_, readErr := validator([]byte(c.Content), c.Context, contentStoredRead)
		if (publishErr != nil) != (c.PublishStatus >= 400) || (readErr != nil) != (c.ReadStatus >= 400) {
			return fmt.Errorf("content boundary preservation failed for %q during production publication/read proof; capabilities are withheld because canonical evidence could be lost or legacy content refused; restore predecode dispatch and strict validation without fallback, then retry", c.Name)
		}
	}
	return nil
}
