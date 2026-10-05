package round

import (
	"reflect"
	"testing"
)

func TestSnapshotRoundTripPreservesRenderedContentAndAssets(t *testing.T) {
	r := Round{Content: "<p>frozen</p>", Decide: "<p>new</p>", Anchors: []string{"a"}}
	files := []File{{Name: "asset.svg", Body: []byte("<svg/>")}}
	b, err := EncodeSnapshot(r, files)
	if err != nil {
		t.Fatal(err)
	}
	got, gotFiles, err := DecodeSnapshot(b)
	if err != nil {
		t.Fatal(err)
	}
	want := Round{Content: "<p>frozen</p>", Anchors: []string{"a"}}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotFiles, files) {
		t.Fatalf("got %#v %#v", got, gotFiles)
	}
}
