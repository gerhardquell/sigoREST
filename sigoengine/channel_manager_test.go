//**********************************************************************
//      sigoengine/channel_manager_test.go
//**********************************************************************

package sigoengine

import (
	"os"
	"testing"
)

func TestChannelManager_Resolve(t *testing.T) {
	os.Setenv("MAMMOUTH_API_KEY", "default-key")
	os.Setenv("MAMMOUTH_API_KEY_0", "key-0")
	defer func() {
		os.Unsetenv("MAMMOUTH_API_KEY")
		os.Unsetenv("MAMMOUTH_API_KEY_0")
	}()

	reg := NewChannelRegistry("")
	reg.DiscoverFromEnv()
	mgr := NewChannelManager(reg)

	ch, err := mgr.Resolve("mammouth", "")
	if err != nil || ch.Name != "default" {
		t.Fatalf("expected default channel, got %+v, err=%v", ch, err)
	}

	_, err = mgr.Resolve("mammouth", "0")
	if err == nil {
		t.Fatal("expected error for inactive channel 0")
	}

	// Full name resolution
	ch, err = mgr.Resolve("mammouth", "mammouth-default")
	if err != nil || ch.Name != "default" {
		t.Fatalf("expected default channel via full name, got %+v, err=%v", ch, err)
	}

	reg.SetActive("mammouth", "0", true)
	ch, err = mgr.Resolve("mammouth", "mammouth-0")
	if err != nil || ch.Name != "0" {
		t.Fatalf("expected channel 0 via full name, got %+v, err=%v", ch, err)
	}
}

func TestChannelManager_NextActive(t *testing.T) {
	os.Setenv("MAMMOUTH_API_KEY", "default-key")
	os.Setenv("MAMMOUTH_API_KEY_0", "key-0")
	os.Setenv("MAMMOUTH_API_KEY_1", "key-1")
	defer func() {
		os.Unsetenv("MAMMOUTH_API_KEY")
		os.Unsetenv("MAMMOUTH_API_KEY_0")
		os.Unsetenv("MAMMOUTH_API_KEY_1")
	}()

	reg := NewChannelRegistry("")
	reg.DiscoverFromEnv()
	reg.SetActive("mammouth", "0", true)
	reg.SetActive("mammouth", "1", true)
	mgr := NewChannelManager(reg)

	def, _ := reg.GetChannel("mammouth", "default")
	ch0, _ := reg.GetChannel("mammouth", "0")
	ch1, _ := reg.GetChannel("mammouth", "1")

	next, ok := mgr.NextActive("mammouth", def)
	if !ok || next.Name != "0" {
		t.Fatalf("expected next after default to be 0, got %+v", next)
	}

	next, ok = mgr.NextActive("mammouth", ch0)
	if !ok || next.Name != "1" {
		t.Fatalf("expected next after 0 to be 1, got %+v", next)
	}

	_, ok = mgr.NextActive("mammouth", ch1)
	if ok {
		t.Fatal("expected no next channel after 1")
	}
}
