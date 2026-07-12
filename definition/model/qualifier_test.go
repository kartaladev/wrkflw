package model_test

import (
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/kartaladev/wrkflw/definition/model"
)

func TestQualifierConstructorsAndString(t *testing.T) {
	cases := []struct {
		name     string
		q        model.Qualifier
		isLatest bool
		str      string
	}{
		{"latest", model.Latest("order"), true, "order"},
		{"pinned", model.Version("order", 3), false, "order:3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert := func(cond bool, msg string) {
				t.Helper()
				if !cond {
					t.Fatalf("%s (q=%+v)", msg, tc.q)
				}
			}
			assert(tc.q.IsLatest() == tc.isLatest, "IsLatest mismatch")
			assert(tc.q.String() == tc.str, "String mismatch: got "+tc.q.String())
		})
	}
}

func TestParseQualifier(t *testing.T) {
	cases := []struct {
		in      string
		want    model.Qualifier
		wantErr bool
	}{
		{"order", model.Latest("order"), false},
		{"order:3", model.Version("order", 3), false},
		{"", model.Qualifier{}, true},
		{":3", model.Qualifier{}, true},
		{"order:", model.Qualifier{}, true},
		{"order:x", model.Qualifier{}, true},
		{"order:-1", model.Qualifier{}, true},
		{"order:0", model.Qualifier{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert := func(cond bool, msg string) {
				t.Helper()
				if !cond {
					t.Fatalf("%s (in=%q)", msg, tc.in)
				}
			}
			got, err := model.ParseQualifier(tc.in)
			if tc.wantErr {
				assert(err != nil, "expected error")
				assert(errors.Is(err, model.ErrInvalidQualifier), "expected ErrInvalidQualifier")
				return
			}
			assert(err == nil, "unexpected error")
			assert(got == tc.want, "parse mismatch")
		})
	}
}

func TestQualifierJSONRoundTrip(t *testing.T) {
	for _, q := range []model.Qualifier{model.Latest("order"), model.Version("order", 3)} {
		b, err := json.Marshal(q)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if want := `"` + q.String() + `"`; string(b) != want {
			t.Fatalf("json = %s, want %s", b, want)
		}
		var back model.Qualifier
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if back != q {
			t.Fatalf("round-trip: got %+v want %+v", back, q)
		}
	}
}

func TestQualifierYAMLRoundTrip(t *testing.T) {
	type holder struct {
		Ref model.Qualifier `yaml:"ref"`
	}
	for _, q := range []model.Qualifier{model.Latest("order"), model.Version("order", 3)} {
		b, err := yaml.Marshal(holder{Ref: q})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// Assert the marshaled YAML bytes match the expected scalar form.
		wantYAML := "ref: " + q.String() + "\n"
		if string(b) != wantYAML {
			t.Fatalf("yaml bytes = %q, want %q", string(b), wantYAML)
		}
		var back holder
		if err := yaml.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if back.Ref != q {
			t.Fatalf("yaml round-trip: got %+v want %+v", back.Ref, q)
		}
	}
}

func TestQualifierUnmarshalRejectsInvalid(t *testing.T) {
	for _, bad := range []string{"order:0", "", "order:x", ":3"} {
		t.Run("json/"+bad, func(t *testing.T) {
			var q model.Qualifier
			err := json.Unmarshal([]byte(strconv.Quote(bad)), &q)
			if !errors.Is(err, model.ErrInvalidQualifier) {
				t.Fatalf("json unmarshal %q: err = %v, want ErrInvalidQualifier", bad, err)
			}
		})
		t.Run("yaml/"+bad, func(t *testing.T) {
			var q model.Qualifier
			err := yaml.Unmarshal([]byte(strconv.Quote(bad)), &q)
			if !errors.Is(err, model.ErrInvalidQualifier) {
				t.Fatalf("yaml unmarshal %q: err = %v, want ErrInvalidQualifier", bad, err)
			}
		})
	}
}

func TestProcessDefinitionQualifier(t *testing.T) {
	def := &model.ProcessDefinition{ID: "order", Version: 3}
	if got := def.Qualifier(); got != model.Version("order", 3) {
		t.Fatalf("def.Qualifier() = %+v", got)
	}
}
