//go:build ignore

package engine

import (
	"fmt"
)

type SimpleSwitch struct{}

func NewSimpleSwitch() (*SimpleSwitch, error) {
	return nil, fmt.Errorf("engine: gont requires Linux")
}

func (s *SimpleSwitch) PingTest() (string, error) {
	return "", fmt.Errorf("engine: gont requires Linux")
}

func (s *SimpleSwitch) Close() error {
	return nil
}