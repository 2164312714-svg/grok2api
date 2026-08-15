package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenyme/grok2api/backend/internal/pkg/imagecontext"
)

func TestBuildImageEditOptionRequestUsesOnlyCandidateMetadata(t *testing.T) {
	input := ImageEditOptionParseInput{
		Instruction: "把上一张改成蓝色竖图，2k", NeedPrompt: true, NeedImageSelection: true,
		NeedAspectRatio: true, NeedResolution: true,
		Candidates: []imagecontext.Candidate{{URL: "TOKEN_SECRET_IMAGE_URL", Description: "previous assistant image"}},
	}
	body, err := buildImageEditOptionRequest(input, "grok-4.6")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "TOKEN_SECRET_IMAGE_URL") {
		t.Fatal("real image URL leaked to the text option parser")
	}
	var payload struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &payload) != nil || len(payload.Messages) != 2 || !strings.Contains(payload.Messages[1].Content, input.Instruction) || !strings.Contains(payload.Messages[1].Content, "multi_image") {
		t.Fatalf("payload = %s", body)
	}
}

func TestDecodeImageEditOptionHintsStrictValidation(t *testing.T) {
	value, err := decodeImageEditOptionHints(`{"prompt":"make it blue","image_indexes":[1],"multi_image":false,"aspect_ratio":"9:16","resolution":"2K","size":null}`, 2)
	if err != nil || value.Prompt == nil || *value.Prompt != "make it blue" || len(value.ImageIndexes) != 1 || value.ImageIndexes[0] != 1 || value.Resolution == nil || *value.Resolution != "2k" {
		t.Fatalf("value = %#v, err = %v", value, err)
	}
	for _, raw := range []string{
		`{"prompt":"x","image_indexes":[2],"aspect_ratio":null,"resolution":null,"size":null}`,
		`{"prompt":"x","image_indexes":[],"aspect_ratio":"7:5","resolution":null,"size":null}`,
		`{"prompt":"x","image_indexes":[],"aspect_ratio":null,"resolution":"4k","size":null}`,
		`{"prompt":"x","image_indexes":[],"aspect_ratio":null,"resolution":null,"size":null,"url":"invented"}`,
	} {
		if _, err := decodeImageEditOptionHints(raw, 2); err == nil {
			t.Fatalf("expected invalid hints for %s", raw)
		}
	}
}

func TestDecodeImageEditOptionHintsLimitsOrdinaryEditsToModelPreferredImage(t *testing.T) {
	value, err := decodeImageEditOptionHints(`{"prompt":null,"image_indexes":[2,1,0],"multi_image":false,"aspect_ratio":null,"resolution":null,"size":null}`, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(value.ImageIndexes) != 1 || value.ImageIndexes[0] != 2 {
		t.Fatalf("indexes = %#v", value.ImageIndexes)
	}
}

func TestDecodeImageEditOptionHintsPreservesExplicitMultiImageSelection(t *testing.T) {
	value, err := decodeImageEditOptionHints(`{"prompt":null,"image_indexes":[2,0,2],"multi_image":true,"aspect_ratio":null,"resolution":null,"size":null}`, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !value.MultiImage || len(value.ImageIndexes) != 2 || value.ImageIndexes[0] != 2 || value.ImageIndexes[1] != 0 {
		t.Fatalf("value = %#v", value)
	}
}
