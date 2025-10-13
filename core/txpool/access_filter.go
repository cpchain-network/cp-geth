package txpool

import (
	"errors"
	"fmt"
	"github.com/holiman/uint256"
	"math"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/contract/access_filter"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

type CallContext struct {
	Statedb      *state.StateDB
	Header       *types.Header
	ChainContext core.ChainContext
	ChainConfig  *params.ChainConfig
}

type AccessFilter struct {
	contract         common.Address
	filterAddressMap map[common.Address]bool
	cacheExpireAt    *time.Time
}

func NewAccessFilter(contract common.Address) *AccessFilter {
	log.Debug("NewAccessFilter ", contract.String())

	return &AccessFilter{contract: contract, filterAddressMap: make(map[common.Address]bool)}
}

func (a *AccessFilter) IsFiltered(addr common.Address) bool {
	exist, _ := a.filterAddressMap[addr]
	return exist
}

func (a *AccessFilter) RefreshCacheIfExpire(ctx *CallContext) error {
	now := time.Now()
	if a.cacheExpireAt == nil || now.After(*a.cacheExpireAt) {
		return a.refreshCache(ctx)
	}

	return nil
}

func (a *AccessFilter) refreshCache(ctx *CallContext) error {
	const method = "GetAllFilteredAddresses"
	result, err := contractReadAll(ctx, a.contract, method)
	if err != nil {
		log.Error("GetAllFilteredAddresses contractReadAll failed", "err", err)
		return err
	}

	newAddressMap := make(map[common.Address]bool)

	for _, v := range result {
		address, ok := v.(common.Address)
		if !ok {
			log.Error("GetAllFilteredAddresses result invalid address")
			continue
		}

		newAddressMap[address] = true
	}

	expireAt := time.Now().Add(5 * time.Minute)

	a.filterAddressMap = newAddressMap
	a.cacheExpireAt = &expireAt
	return nil
}

// contractRead perform contract read
func contractRead(ctx *CallContext, contract common.Address, method string, args ...interface{}) (interface{}, interface{}, error) {
	ret, err := contractReadAll(ctx, contract, method, args...)
	if err != nil {
		return nil, nil, err
	}
	if len(ret) != 2 {
		return nil, nil, errors.New(method + ": invalid result length")
	}
	return ret[0], ret[1], nil
}

// contractReadAll perform contract Read and return all results
func contractReadAll(ctx *CallContext, contract common.Address, method string, args ...interface{}) ([]interface{}, error) {
	abi := access_filter.ABI()
	result, err := contractReadBytes(ctx, contract, &abi, method, args...)
	if err != nil {
		return nil, err
	}
	// unpack data
	ret, err := abi.Unpack(method, result)
	if err != nil {
		return nil, err
	}
	return ret, nil
}

// contractReadBytes perform read contract and returns bytes
func contractReadBytes(ctx *CallContext, contract common.Address, abi *abi.ABI, method string, args ...interface{}) ([]byte, error) {
	data, err := abi.Pack(method, args...)
	if err != nil {
		log.Error("Can't pack data", "method", method, "error", err)
		return nil, err
	}
	result, err := CallContract(ctx, &contract, data)
	if err != nil {
		log.Error("Failed to execute", "method", method, "err", err)
		return nil, err
	}
	return result, nil
}

// CallContract executes transaction sent to system contracts.
func CallContract(ctx *CallContext, to *common.Address, data []byte) (ret []byte, err error) {
	return callContractWithValue(ctx /*ctx.Header.Coinbase*/, *to, to, data, big.NewInt(0))
}

// CallContract executes transaction sent to system contracts.
func callContractWithValue(ctx *CallContext, from common.Address, to *common.Address, data []byte, value *big.Int) (ret []byte, err error) {
	blockCtx := core.NewEVMBlockContext(ctx.Header, ctx.ChainContext, nil, nil, nil)
	evm := vm.NewEVM(blockCtx, ctx.Statedb, ctx.ChainConfig, vm.Config{})

	value256, _ := uint256.FromBig(value)
	ret, _, err = evm.Call(from, *to, data, math.MaxUint64, value256)

	return ret, WrapVMError(err, ret)
}

// WrapVMError wraps vm error with readable reason
func WrapVMError(err error, ret []byte) error {
	if errors.Is(err, vm.ErrExecutionReverted) {
		reason, errUnpack := abi.UnpackRevert(common.CopyBytes(ret))
		if errUnpack != nil {
			reason = "internal error"
		}
		return fmt.Errorf("%s: %s", err.Error(), reason)
	}
	return err
}
