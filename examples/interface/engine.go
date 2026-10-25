package main

import "github.com/kartaladev/doger"

type Engine any

type engineImplem struct{}

func NewEngine(i doger.Injector) (*engineImplem, error) {
	return &engineImplem{}, nil
}
