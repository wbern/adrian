package reviewpolicy

import (
	"reflect"
	"testing"
)

func TestParseTagsStrictOptInMetadata(t *testing.T) {
	for _, test := range []struct {
		name, yaml string
		want       []string
	}{
		{"absent", "", []string{}}, {"empty-list", "tags: []\n", []string{}},
		{"scalar", "tags: ux\n", []string{"ux"}},
		{"flow", "tags: [ux, security, ux]\n", []string{"ux", "security"}},
		{"block", "tags:\n  - ux\n  - security\n", []string{"ux", "security"}},
		{"trim-and-preserve", "tags: [' ux ', UX, 'é', 'é', '宇宙', ' ux ']\n", []string{"ux", "UX", "é", "é", "宇宙"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseTags([]byte("---\nstatus: accepted\n" + test.yaml + "---\n# Rule\n"))
			if err != nil {
				t.Fatal(err)
				return
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %#v, want %#v", got, test.want)
				return
			}
		})
	}
	for _, value := range []string{"null", "~", "", "true", "42", "{}", "[ux, null]", "[ux, false]", "[ux, 3]", "[[ux]]", "[{}]", "''", "'   '", "['']", "['  ']", `"\tux"`, `"ux\n"`, `"\u0085ux"`, `"ux\u007f"`} {
		t.Run("refuse-"+value, func(t *testing.T) {
			if _, err := ParseTags([]byte("---\nstatus: accepted\ntags: " + value + "\n---\n# Rule\n")); err == nil {
				t.Fatalf("accepted malformed tags %s", value)
				return
			}
		})
	}
}
