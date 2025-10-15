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

var (
	defaultAddress = "0x39Af025d0F1982fb547DC550549162Edd7701E36"
	testnetAddress = "0x39Af025d0F1982fb547DC550549162Edd7701E36"
	devnetAddress  = "0x39Af025d0F1982fb547DC550549162Edd7701E36"
	mainnetAddress = "0x"
)

var chainIDMap = map[*big.Int]string{
	big.NewInt(86608):   mainnetAddress,
	big.NewInt(86606):   testnetAddress,
	big.NewInt(7654321): devnetAddress,
}

type AccessFilter struct {
	contract         common.Address
	filterAddressMap map[common.Address]bool
	cacheExpireAt    *time.Time
}

func NewAccessFilter(chainID *big.Int) *AccessFilter {
	contract, ok := chainIDMap[chainID]
	if !ok {
		contract = defaultAddress
	}
	log.Info("NewAccessFilter ", contract)

	return &AccessFilter{contract: common.HexToAddress(contract), filterAddressMap: make(map[common.Address]bool)}
}

func (a *AccessFilter) IsFiltered(addr common.Address) bool {
	exist, _ := a.filterAddressMap[addr]
	return exist
}

func (a *AccessFilter) RefreshCacheIfExpire(ctx *CallContext) error {
	now := time.Now()
	if a.cacheExpireAt == nil || now.After(*a.cacheExpireAt) {
		return a.RefreshCache(ctx)
	}

	return nil
}

func (a *AccessFilter) RefreshCache(ctx *CallContext) error {
	const method = "getAllFilteredAddresses"
	result, err := contractReadAll(ctx, a.contract, method)
	if err != nil {
		log.Error("getAllFilteredAddresses contractReadAll failed", "err", err)
		return err
	}

	newAddressMap := make(map[common.Address]bool)

	filterAddresses := make([]common.Address, 0)
	if len(result) > 0 {
		filterAddresses, _ = result[0].([]common.Address)
	}

	for _, v := range filterAddresses {
		newAddressMap[v] = true
	}

	expireAt := time.Now().Add(5 * time.Second)

	a.filterAddressMap = newAddressMap
	a.cacheExpireAt = &expireAt
	return nil
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
	blockCtx := core.NewEVMBlockContext(ctx.Header, ctx.ChainContext, nil, ctx.ChainConfig, nil)
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
