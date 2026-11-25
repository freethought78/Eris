package uinput

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Plugs in a virtual controller, checks the kernel sees it, and unplugs it.
// No button presses are sent. Only runs with ERIS_UINPUT_TEST=1, because it
// briefly adds a controller to the system.
func TestCreate(t *testing.T) {
	if os.Getenv("ERIS_UINPUT_TEST") != "1" {
		t.Skip("set ERIS_UINPUT_TEST=1 to run")
	}
	p, err := Create("Eris Prototype Pad")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	devs, _ := os.ReadFile("/proc/bus/input/devices")
	found := strings.Contains(string(devs), `Name="Eris Prototype Pad"`)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("kernel doesn't list the virtual controller")
	}
	time.Sleep(200 * time.Millisecond)
	devs, _ = os.ReadFile("/proc/bus/input/devices")
	if strings.Contains(string(devs), `Name="Eris Prototype Pad"`) {
		t.Fatal("virtual controller still listed after Close")
	}
}
