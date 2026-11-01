package nativerunner

import (
	"reflect"
	"testing"
)

func TestBwrapArgumentsIsolatesNetworkAndRoot(t *testing.T) {
	got := bwrapArguments("/usr/bin/echo", []string{"hello"}, "/tmp/eki-run-1")
	want := []string{
		"--die-with-parent",
		"--unshare-net",
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--bind", "/tmp/eki-run-1", "/tmp/eki-run-1",
		"--chdir", "/tmp/eki-run-1",
		"--", "/usr/bin/echo", "hello",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v want=%v", got, want)
	}
}

func TestBwrapArgumentsWithoutWorkDir(t *testing.T) {
	got := bwrapArguments("/usr/bin/echo", nil, "")
	want := []string{
		"--die-with-parent",
		"--unshare-net",
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--", "/usr/bin/echo",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v want=%v", got, want)
	}
}
