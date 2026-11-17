package v1alpha1

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSkillList_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want SkillList
	}{
		{"list", `["a","b"]`, SkillList{"a", "b"}},
		{"empty list", `[]`, SkillList{}},
		{"null", `null`, nil},
		{"legacy wrapper", `{"items":["a","b"]}`, SkillList{"a", "b"}},
		{"legacy empty wrapper", `{}`, SkillList{}},
		{"legacy wrapper with null items", `{"items":null}`, SkillList{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got SkillList
			if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("unmarshal %s = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSkillList_UnmarshalJSON_RejectsOtherShapes(t *testing.T) {
	for _, in := range []string{`"a"`, `{"other":["a"]}`, `{"items":"a"}`, `[1,2]`} {
		var got SkillList
		if err := json.Unmarshal([]byte(in), &got); err == nil {
			t.Errorf("unmarshal %s: want an error, got %#v", in, got)
		}
	}
}

// A whole Agent stored in the old shape decodes, so one unmigrated object no
// longer fails a typed List of every agent.
func TestAgent_DecodesLegacySkillsWrapper(t *testing.T) {
	raw := `{"apiVersion":"ainsel.dev/v1alpha1","kind":"Agent","metadata":{"name":"a"},
		"spec":{"displayName":"a","imageRef":{"name":"i"},"llm":{"model":"m"},"persona":{"id":"p"},"runtime":{},
		"skills":{"items":["skill-on-agent"]}}}`
	var a Agent
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(a.Spec.Skills, SkillList{"skill-on-agent"}) {
		t.Errorf("skills = %#v", a.Spec.Skills)
	}
	out, err := json.Marshal(a.Spec.Skills)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `["skill-on-agent"]` {
		t.Errorf("marshal = %s, want the plain list", out)
	}
}
