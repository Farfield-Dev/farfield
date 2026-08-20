package canonicaljson

import "testing"

func TestNormalize(t *testing.T) {
	t.Parallel()
	got, err := Normalize([]byte(` { "z": 1, "text": "<agent>", "a": [true, null] } `))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":[true,null],"text":"<agent>","z":1}`
	if string(got) != want {
		t.Fatalf("Normalize() = %s, want %s", got, want)
	}
}

func TestNormalizeRejectsTrailingValue(t *testing.T) {
	t.Parallel()
	if _, err := Normalize([]byte(`{} {}`)); err == nil {
		t.Fatal("Normalize accepted two JSON values")
	}
}

func TestNormalizeUsesRFC8785NumberAndUnicodeEncoding(t *testing.T) {
	t.Parallel()
	input := []byte(`{"numbers":[333333333.33333329,1E30,4.50,2e-3,1e-27],"string":"€"}`)
	got, err := Normalize(input)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],"string":"€"}`
	if string(got) != want {
		t.Fatalf("Normalize() = %s, want %s", got, want)
	}
}
