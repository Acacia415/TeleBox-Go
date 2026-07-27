package repeat

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	messageCount, repeatCount, err := parseArgs([]string{"3", "2"})
	if err != nil {
		t.Fatal(err)
	}
	if messageCount != 3 || repeatCount != 2 {
		t.Fatalf("counts = %d, %d", messageCount, repeatCount)
	}
	for _, args := range [][]string{{"0"}, {"101"}, {"1", "11"}, {"x"}} {
		if _, _, err := parseArgs(args); err == nil {
			t.Fatalf("parseArgs(%v) succeeded", args)
		}
	}
}

func TestMessageRange(t *testing.T) {
	if got, want := messageRange(12, 3), []int{10, 11, 12}; !reflect.DeepEqual(got, want) {
		t.Fatalf("messageRange = %v, want %v", got, want)
	}
	if got, want := messageRange(2, 5), []int{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("messageRange at start = %v, want %v", got, want)
	}
}
