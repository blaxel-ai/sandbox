package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func main() {
	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(2)
	}
	err = unix.FinitModule(int(f.Fd()), strings.Join(os.Args[2:], " "), unix.MODULE_INIT_IGNORE_MODVERSIONS|unix.MODULE_INIT_IGNORE_VERMAGIC)
	fmt.Println("finit_module:", err)
	if err == nil {
		b, _ := os.ReadFile("/sys/module/virtio_ring_resync/parameters/resynced")
		fmt.Println("resynced:", strings.TrimSpace(string(b)))
		fmt.Println("delete_module:", unix.DeleteModule("virtio_ring_resync", 0))
	}
}
