//go:build !unix

package main

import "errors"

func diskFree(string) (int64, error) { return 0, errors.New("nicht messbar") }

func diskSize(string) (int64, int64, error) { return 0, 0, errors.New("nicht messbar") }
func diskDF(string) (int64, int64, error)   { return 0, 0, errors.New("nicht messbar") }
func diskReserve(string) int64              { return 0 }
