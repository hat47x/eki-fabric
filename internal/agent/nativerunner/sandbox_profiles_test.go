package nativerunner

import "testing"

func TestProfileDeniesNetwork(t *testing.T) {
	if !profileDeniesNetwork(SandboxProfileLinuxBwrap) {
		t.Fatal("linux-bwrap must deny network")
	}
	if profileDeniesNetwork(SandboxProfile) {
		t.Fatal("native-fixed-spike must not claim network isolation")
	}
	if profileDeniesNetwork(SandboxProfileJobObject) {
		t.Fatal("native-job-object must not claim network isolation")
	}
}
