//go:build !linux || !cgo

package uvc

import "fmt"

type Device struct{ Path string }

func Open(string, int, int, int, string) (*Device, error) {
	return nil, fmt.Errorf("UVC output requires Linux with cgo")
}
func (*Device) Write([]byte, int, int) (int, error) {
	return 0, fmt.Errorf("UVC output requires Linux")
}
func (*Device) Mode() (int, int, int)         { return 0, 0, 0 }
func (*Device) Close() error                  { return nil }
func (*Device) Reset()                        {}
func (*Device) Status() (bool, uint64, error) { return false, 0, nil }

func (*Device) WriteH264([]byte, bool) (int, error) {
	return 0, fmt.Errorf("UVC output requires Linux")
}
func (*Device) Recovery() (bool, uint64) { return false, 0 }
