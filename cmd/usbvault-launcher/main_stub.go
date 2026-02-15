//go:build !darwin
// +build !darwin

package main

import "fmt"

func main() {
	fmt.Println("usbvault-launcher is supported on macOS only")
}
