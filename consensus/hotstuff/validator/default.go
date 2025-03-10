/*
 * Copyright (C) 2021 The Zion Authors
 * This file is part of The Zion library.
 *
 * The Zion is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The Zion is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with The Zion.  If not, see <http://www.gnu.org/licenses/>.
 */

package validator

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/hotstuff"
)

var ErrInvalidParticipant = errors.New("invalid participants")

type defaultValidator struct {
	address common.Address
}

func (val *defaultValidator) Address() common.Address {
	return val.address
}

func (val *defaultValidator) String() string {
	return val.Address().String()
}

// ----------------------------------------------------------------------------

type defaultSet struct {
	validators hotstuff.Validators
	policy     hotstuff.SelectProposerPolicy

	proposer    hotstuff.Validator
	validatorMu sync.RWMutex
	selector    hotstuff.ProposalSelector
}

func newDefaultSet(addrs []common.Address, policy hotstuff.SelectProposerPolicy) *defaultSet {
	valSet := &defaultSet{}

	valSet.policy = policy
	// init validators
	valSet.validators = make([]hotstuff.Validator, len(addrs))
	for i, addr := range addrs {
		valSet.validators[i] = New(addr)
	}
	// sort validator
	sort.Sort(valSet.validators)
	// init proposer
	if valSet.Size() > 0 {
		valSet.proposer = valSet.GetByIndex(0)
	}
	valSet.selector = roundRobinSelector
	if policy == hotstuff.Sticky {
		valSet.selector = stickySelector
	}
	if policy == hotstuff.VRF {
		valSet.selector = vrfSelector
	}

	return valSet
}

func (valSet *defaultSet) Size() int {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()
	return len(valSet.validators)
}

func (valSet *defaultSet) List() []hotstuff.Validator {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()
	return valSet.validators
}

func (valSet *defaultSet) AddressList() []common.Address {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()

	vals := make([]common.Address, valSet.Size())
	for i, v := range valSet.List() {
		vals[i] = v.Address()
	}
	return vals
}

func (valSet *defaultSet) GetByIndex(i uint64) hotstuff.Validator {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()
	if i < uint64(valSet.Size()) {
		return valSet.validators[i]
	}
	return nil
}

func (valSet *defaultSet) GetByAddress(addr common.Address) (int, hotstuff.Validator) {
	for i, val := range valSet.List() {
		if addr == val.Address() {
			return i, val
		}
	}
	return -1, nil
}

func (valSet *defaultSet) GetProposer() hotstuff.Validator {
	return valSet.proposer
}

func (valSet *defaultSet) IsProposer(address common.Address) bool {
	_, val := valSet.GetByAddress(address)
	return reflect.DeepEqual(valSet.GetProposer(), val)
}

func (valSet *defaultSet) CalcProposer(lastProposer common.Address, round uint64) {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()
	valSet.proposer = valSet.selector(valSet, lastProposer, round)
}

func (valSet *defaultSet) String() string {
	str := "["
	for _, v := range valSet.validators {
		str += v.Address().Hex() + " "
	}
	str += "]"
	return str
}

func calcSeed(valSet hotstuff.ValidatorSet, proposer common.Address, round uint64) uint64 {
	offset := 0
	if idx, val := valSet.GetByAddress(proposer); val != nil {
		offset = idx
	}
	return uint64(offset) + round
}

func emptyAddress(addr common.Address) bool {
	return addr == common.Address{}
}

func roundRobinSelector(valSet hotstuff.ValidatorSet, proposer common.Address, round uint64) hotstuff.Validator {
	if valSet.Size() == 0 {
		return nil
	}
	seed := uint64(0)
	if emptyAddress(proposer) {
		seed = round
	} else {
		seed = calcSeed(valSet, proposer, round) + 1
	}
	pick := seed % uint64(valSet.Size())
	return valSet.GetByIndex(pick)
}

func stickySelector(valSet hotstuff.ValidatorSet, proposer common.Address, round uint64) hotstuff.Validator {
	if valSet.Size() == 0 {
		return nil
	}
	seed := uint64(0)
	if emptyAddress(proposer) {
		seed = round
	} else {
		seed = calcSeed(valSet, proposer, round)
	}
	pick := seed % uint64(valSet.Size())
	return valSet.GetByIndex(pick)
}

// TODO: implement VRF
func vrfSelector(valSet hotstuff.ValidatorSet, proposer common.Address, round uint64) hotstuff.Validator {
	return nil
}

func (valSet *defaultSet) AddValidator(address common.Address) bool {
	valSet.validatorMu.Lock()
	defer valSet.validatorMu.Unlock()
	for _, v := range valSet.validators {
		if v.Address() == address {
			return false
		}
	}
	valSet.validators = append(valSet.validators, New(address))
	// TODO: we may not need to re-sort it again
	// sort validator
	sort.Sort(valSet.validators)
	return true
}

func (valSet *defaultSet) RemoveValidator(address common.Address) bool {
	valSet.validatorMu.Lock()
	defer valSet.validatorMu.Unlock()

	for i, v := range valSet.validators {
		if v.Address() == address {
			valSet.validators = append(valSet.validators[:i], valSet.validators[i+1:]...)
			return true
		}
	}
	return false
}

func (valSet *defaultSet) Copy() hotstuff.ValidatorSet {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()

	addresses := make([]common.Address, 0, len(valSet.validators))
	for _, v := range valSet.validators {
		addresses = append(addresses, v.Address())
	}
	return NewSet(addresses, valSet.policy)
}

func (valSet *defaultSet) ParticipantsNumber(list []common.Address) int {
	if list == nil || len(list) == 0 {
		return 0
	}
	size := 0
	for _, v := range list {
		if index, _ := valSet.GetByAddress(v); index < 0 {
			continue
		} else {
			size += 1
		}
	}
	return size
}

func (valSet *defaultSet) CheckQuorum(committers []common.Address) error {
	if committers == nil {
		return ErrInvalidParticipant
	}

	quorum := valSet.Q()
	validSeal := 0
	validators := valSet.Copy()

	for _, addr := range committers {
		if validators.RemoveValidator(addr) {
			validSeal++
			continue
		}
	}

	// The length of validSeal should not be smaller than number of `n - f`
	if validSeal < quorum {
		return fmt.Errorf("valid seal number %v < quorum number %v", validSeal, quorum)
	}
	return nil
}

func (valSet *defaultSet) F() int { return (valSet.Size() - 1) / 3 }

func (valSet *defaultSet) Q() int { return valSet.Size() - valSet.F() }

func (valSet *defaultSet) Policy() hotstuff.SelectProposerPolicy { return valSet.policy }

func (valSet *defaultSet) Cmp(src hotstuff.ValidatorSet) bool {
	n := valSet.ParticipantsNumber(src.AddressList())
	if n != valSet.Size() || n != src.Size() {
		return false
	}
	return true
}

func (valSet *defaultSet) Equal(src hotstuff.ValidatorSet) bool {
	for _, v := range valSet.validators {
		if index, _ := src.GetByAddress(v.Address()); index < 0 {
			return false
		}
	}
	for _, v := range src.AddressList() {
		if index, _ := valSet.GetByAddress(v); index < 0 {
			return false
		}
	}

	return true
}
