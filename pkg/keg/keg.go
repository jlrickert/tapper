package keg

import "sync"

type Keg struct {
	repo KegRepository
	lock sync.Mutex
}

func LookupKeg() (Keg, error) {
	return Keg{}, nil
}

func NewKeg(repo KegRepository) Keg {
	return Keg{
		repo: repo,
		lock: sync.Mutex{},
	}
}

func (keg *Keg) UpdateIndex() error {
	return nil
}
