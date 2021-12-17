package main

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadConfig(t *testing.T) {

}

func TestFileDump(t *testing.T) {
	f := File{
		Path:        "/foo/bar",
		Permissions: "0777",
		Contents:    "hi",
	}

	var buf bytes.Buffer
	err := yaml.NewEncoder(&buf).Encode(f)
	if err != nil {
		t.Fatal(err)
	}

	var result File
	err = yaml.NewDecoder(&buf).Decode(&result)

	if f.Path != result.Path {
		t.Errorf("wanted path to be %q, got: %q", f.Path, result.Path)
	}

	if f.Permissions != result.Permissions {
		t.Errorf("wanted permissions to be %q, got: %q", f.Permissions, result.Permissions)
	}

	if f.Contents != result.Contents {
		t.Error("wanted contents to be equal but it wasn't")
		t.Errorf("input  %s", f.Contents)
		t.Errorf("output %s", result.Contents)
	}
}

func TestFileApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testfoo")

	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	g, err := user.LookupGroupId(u.Gid)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(u.Username, g.Name)

	f := File{
		Path:        path,
		Permissions: "0777",
		Contents:    "test foo",
		Owner:       u.Username,
		Group:       g.Name,
	}

	err = f.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != f.Contents {
		t.Fatalf("wanted file contents to be %q, got: %q", f.Contents, string(data))
	}
}
