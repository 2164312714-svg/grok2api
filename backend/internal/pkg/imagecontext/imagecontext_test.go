package imagecontext

import "testing"

func TestParseOrdersCurrentThenHistoricalImages(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":[{"type":"text","text":"old"},{"type":"image_url","image_url":{"url":"https://example.com/old.png"}}]},
		{"role":"assistant","content":"完成 ![result](https://example.com/result.png)"},
		{"role":"user","content":[{"type":"text","text":"把这张图改成蓝色，16:9，2k"},{"type":"input_image","image_url":"https://example.com/new.png"}]}
	]}`)
	value, err := Parse(body, "chat")
	if err != nil {
		t.Fatal(err)
	}
	if value.Prompt != "把这张图改成蓝色，16:9，2k" || len(value.Candidates) != 3 {
		t.Fatalf("value = %#v", value)
	}
	if value.Candidates[0].URL != "https://example.com/new.png" || value.Candidates[1].URL != "https://example.com/result.png" || value.Candidates[2].URL != "https://example.com/old.png" {
		t.Fatalf("candidate order = %#v", value.Candidates)
	}
	if value.Candidates[1].Description != "previous assistant image 1 (newest historical)" || value.Candidates[2].Description != "previous user image 1 (newest historical)" {
		t.Fatalf("candidate descriptions = %#v", value.Candidates)
	}
}

func TestParseTreatsReplayedAssistantImageAsValidEditInput(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"![image](https://example.com/result.png)"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"make the previous image blue"},{"type":"input_image","image_url":"https://example.com/result.png"}]}
	]}`)
	value, err := Parse(body, "responses")
	if err != nil || len(value.Candidates) != 1 || value.Candidates[0].URL != "https://example.com/result.png" {
		t.Fatalf("value = %#v, err = %v", value, err)
	}
}
