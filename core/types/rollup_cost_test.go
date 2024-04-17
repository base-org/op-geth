package types

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

var (
	baseFee  = big.NewInt(1000 * 1e6)
	overhead = big.NewInt(50)
	scalar   = big.NewInt(7 * 1e6)

	blobBaseFee       = big.NewInt(10 * 1e6)
	baseFeeScalar     = big.NewInt(2)
	blobBaseFeeScalar = big.NewInt(3)

	// below are the expected cost func outcomes for the above parameter settings on the emptyTx
	// which is defined in transaction_test.go
	bedrockFee  = big.NewInt(11326000000000)
	regolithFee = big.NewInt(3710000000000)
	ecotoneFee  = big.NewInt(960900) // (480/16)*(2*16*1000 + 3*10) == 960900
	fjordFee    = big.NewInt(63852)  // (-27321890 + 1031462*31 - 88664*30) * (2*16*1000 + 3*10) / 1e6 == 63852

	bedrockGas  = big.NewInt(1618)
	regolithGas = big.NewInt(530) // 530  = 1618 - (16*68)
	ecotoneGas  = big.NewInt(480)
	fjordGas    = ecotoneGas
)

func TestBedrockL1CostFunc(t *testing.T) {
	costFunc0 := newL1CostFuncBedrockHelper(baseFee, overhead, scalar, false /*isRegolith*/)
	costFunc1 := newL1CostFuncBedrockHelper(baseFee, overhead, scalar, true)

	c0, g0 := costFunc0(emptyTx.RollupCostData()) // pre-Regolith
	c1, g1 := costFunc1(emptyTx.RollupCostData())

	require.Equal(t, bedrockFee, c0)
	require.Equal(t, bedrockGas, g0) // gas-used

	require.Equal(t, regolithFee, c1)
	require.Equal(t, regolithGas, g1)
}

func TestEcotoneL1CostFunc(t *testing.T) {
	costFunc := newL1CostFuncEcotone(baseFee, blobBaseFee, baseFeeScalar, blobBaseFeeScalar)

	c0, g0 := costFunc(emptyTx.RollupCostData())

	require.Equal(t, ecotoneGas, g0)
	require.Equal(t, ecotoneFee, c0)
}

func TestFjordL1CostFunc(t *testing.T) {
	costFunc := newL1CostFuncFjord(
		baseFee,
		blobBaseFee,
		baseFeeScalar,
		blobBaseFeeScalar,
		l1CostIntercept,
		l1CostFastlzCoef,
		l1CostTxSizeCoef,
	)

	c0, g0 := costFunc(emptyTx.RollupCostData())

	require.Equal(t, fjordGas, g0)
	require.Equal(t, fjordFee, c0)
}

func TestExtractBedrockGasParams(t *testing.T) {
	regolithTime := uint64(1)
	config := &params.ChainConfig{
		Optimism:     params.OptimismTestConfig.Optimism,
		RegolithTime: &regolithTime,
	}

	data := getBedrockL1Attributes(baseFee, overhead, scalar)

	gasparams, err := extractL1GasParams(config, regolithTime-1, data)
	costFuncPreRegolith := gasparams.costFunc
	require.NoError(t, err)

	// Function should continue to succeed even with extra data (that just gets ignored) since we
	// have been testing the data size is at least the expected number of bytes instead of exactly
	// the expected number of bytes. It's unclear if this flexibility was intentional, but since
	// it's been in production we shouldn't change this behavior.
	data = append(data, []byte{0xBE, 0xEE, 0xEE, 0xFF}...) // tack on garbage data
	gasparams, err = extractL1GasParams(config, regolithTime, data)
	costFuncRegolith := gasparams.costFunc
	require.NoError(t, err)

	c, _ := costFuncPreRegolith(emptyTx.RollupCostData())
	require.Equal(t, bedrockFee, c)

	c, _ = costFuncRegolith(emptyTx.RollupCostData())
	require.Equal(t, regolithFee, c)

	// try to extract from data which has not enough params, should get error.
	data = data[:len(data)-4-32]
	_, err = extractL1GasParams(config, regolithTime, data)
	require.Error(t, err)
}

func TestExtractEcotoneGasParams(t *testing.T) {
	zeroTime := uint64(0)
	// create a config where ecotone upgrade is active
	config := &params.ChainConfig{
		Optimism:     params.OptimismTestConfig.Optimism,
		RegolithTime: &zeroTime,
		EcotoneTime:  &zeroTime,
	}
	require.True(t, config.IsOptimismEcotone(zeroTime))

	data := getEcotoneL1Attributes(
		baseFee,
		blobBaseFee,
		baseFeeScalar,
		blobBaseFeeScalar,
	)

	gasparams, err := extractL1GasParams(config, zeroTime, data)
	require.NoError(t, err)
	costFunc := gasparams.costFunc

	c, g := costFunc(emptyTx.RollupCostData())

	require.Equal(t, ecotoneGas, g)
	require.Equal(t, ecotoneFee, c)

	// make sure wrong amont of data results in error
	data = append(data, 0x00) // tack on garbage byte
	_, err = extractL1GasParamsEcotone(data)
	require.Error(t, err)
}

// make sure the first block of the ecotone upgrade is properly detected, and invokes the bedrock
// cost function appropriately
func TestFirstBlockEcotoneGasParams(t *testing.T) {
	zeroTime := uint64(0)
	// create a config where ecotone upgrade is active
	config := &params.ChainConfig{
		Optimism:     params.OptimismTestConfig.Optimism,
		RegolithTime: &zeroTime,
		EcotoneTime:  &zeroTime,
	}
	require.True(t, config.IsOptimismEcotone(0))

	data := getBedrockL1Attributes(baseFee, overhead, scalar)

	gasparams, err := extractL1GasParams(config, zeroTime, data)
	require.NoError(t, err)
	oldCostFunc := gasparams.costFunc
	c, g := oldCostFunc(emptyTx.RollupCostData())
	require.Equal(t, regolithGas, g)
	require.Equal(t, regolithFee, c)
}

func getBedrockL1Attributes(baseFee, overhead, scalar *big.Int) []byte {
	uint256 := make([]byte, 32)
	ignored := big.NewInt(1234)
	data := []byte{}
	data = append(data, BedrockL1AttributesSelector...)
	data = append(data, ignored.FillBytes(uint256)...)  // arg 0
	data = append(data, ignored.FillBytes(uint256)...)  // arg 1
	data = append(data, baseFee.FillBytes(uint256)...)  // arg 2
	data = append(data, ignored.FillBytes(uint256)...)  // arg 3
	data = append(data, ignored.FillBytes(uint256)...)  // arg 4
	data = append(data, ignored.FillBytes(uint256)...)  // arg 5
	data = append(data, overhead.FillBytes(uint256)...) // arg 6
	data = append(data, scalar.FillBytes(uint256)...)   // arg 7
	return data
}

func getEcotoneL1Attributes(baseFee, blobBaseFee, baseFeeScalar, blobBaseFeeScalar *big.Int) []byte {
	ignored := big.NewInt(1234)
	data := []byte{}
	uint256Slice := make([]byte, 32)
	uint64Slice := make([]byte, 8)
	uint32Slice := make([]byte, 4)
	data = append(data, EcotoneL1AttributesSelector...)
	data = append(data, baseFeeScalar.FillBytes(uint32Slice)...)
	data = append(data, blobBaseFeeScalar.FillBytes(uint32Slice)...)
	data = append(data, ignored.FillBytes(uint64Slice)...)
	data = append(data, ignored.FillBytes(uint64Slice)...)
	data = append(data, ignored.FillBytes(uint64Slice)...)
	data = append(data, baseFee.FillBytes(uint256Slice)...)
	data = append(data, blobBaseFee.FillBytes(uint256Slice)...)
	data = append(data, ignored.FillBytes(uint256Slice)...)
	data = append(data, ignored.FillBytes(uint256Slice)...)
	return data
}

type testStateGetter struct {
	baseFee, blobBaseFee, overhead, scalar *big.Int
	baseFeeScalar, blobBaseFeeScalar       uint32
}

func (sg *testStateGetter) GetState(addr common.Address, slot common.Hash) common.Hash {
	buf := common.Hash{}
	switch slot {
	case L1BaseFeeSlot:
		sg.baseFee.FillBytes(buf[:])
	case OverheadSlot:
		sg.overhead.FillBytes(buf[:])
	case ScalarSlot:
		sg.scalar.FillBytes(buf[:])
	case L1BlobBaseFeeSlot:
		sg.blobBaseFee.FillBytes(buf[:])
	case L1FeeScalarsSlot:
		// fetch Ecotone fee sclars
		offset := scalarSectionStart
		binary.BigEndian.PutUint32(buf[offset:offset+4], sg.baseFeeScalar)
		binary.BigEndian.PutUint32(buf[offset+4:offset+8], sg.blobBaseFeeScalar)
	default:
		panic("unknown slot")
	}
	return buf
}

// TestNewL1CostFunc tests that the appropriate cost function is selected based on the
// configuration and statedb values.
func TestNewL1CostFunc(t *testing.T) {
	time := uint64(10)
	timeInFuture := uint64(20)
	config := &params.ChainConfig{
		Optimism: params.OptimismTestConfig.Optimism,
	}
	statedb := &testStateGetter{
		baseFee:           baseFee,
		overhead:          overhead,
		scalar:            scalar,
		blobBaseFee:       blobBaseFee,
		baseFeeScalar:     uint32(baseFeeScalar.Uint64()),
		blobBaseFeeScalar: uint32(blobBaseFeeScalar.Uint64()),
	}

	costFunc := NewL1CostFunc(config, statedb)
	require.NotNil(t, costFunc)

	// empty cost data should result in nil fee
	fee := costFunc(RollupCostData{}, time)
	require.Nil(t, fee)

	// emptyTx fee w/ bedrock config should be the bedrock fee
	fee = costFunc(emptyTx.RollupCostData(), time)
	require.NotNil(t, fee)
	require.Equal(t, bedrockFee, fee)

	// emptyTx fee w/ regolith config should be the regolith fee
	config.RegolithTime = &time
	costFunc = NewL1CostFunc(config, statedb)
	require.NotNil(t, costFunc)
	fee = costFunc(emptyTx.RollupCostData(), time)
	require.NotNil(t, fee)
	require.Equal(t, regolithFee, fee)

	// emptyTx fee w/ ecotone config should be the ecotone fee
	config.EcotoneTime = &time
	costFunc = NewL1CostFunc(config, statedb)
	fee = costFunc(emptyTx.RollupCostData(), time)
	require.NotNil(t, fee)
	require.Equal(t, ecotoneFee, fee)

	// emptyTx fee w/ fjord config should be the fjord fee
	config.FjordTime = &time
	costFunc = NewL1CostFunc(config, statedb)
	fee = costFunc(emptyTx.RollupCostData(), time)
	require.NotNil(t, fee)
	require.Equal(t, fjordFee, fee)

	// emptyTx fee w/ ecotone config, but simulate first ecotone block by blowing away the ecotone
	// params. Should result in regolith fee.
	config.FjordTime = &timeInFuture
	statedb.baseFeeScalar = 0
	statedb.blobBaseFeeScalar = 0
	statedb.blobBaseFee = new(big.Int)
	costFunc = NewL1CostFunc(config, statedb)
	fee = costFunc(emptyTx.RollupCostData(), time)
	require.NotNil(t, fee)
	require.Equal(t, regolithFee, fee)

	// emptyTx fee w/ fjord config, but simulate first ecotone block by blowing away the ecotone
	// params. Should result in regolith fee.
	config.EcotoneTime = &time
	config.FjordTime = &time
	statedb.baseFeeScalar = 0
	statedb.blobBaseFeeScalar = 0
	statedb.blobBaseFee = new(big.Int)
	costFunc = NewL1CostFunc(config, statedb)
	fee = costFunc(emptyTx.RollupCostData(), time)
	require.NotNil(t, fee)
	require.Equal(t, regolithFee, fee)
}

func TestFlzCompressLen(t *testing.T) {
	var (
		emptyTxBytes, _   = emptyTx.MarshalBinary()
		contractCallTxStr = "02f901550a758302df1483be21b88304743f94f8" +
			"0e51afb613d764fa61751affd3313c190a86bb870151bd62fd12adb8" +
			"e41ef24f3f0000000000000000000000000000000000000000000000" +
			"00000000000000006e000000000000000000000000af88d065e77c8c" +
			"c2239327c5edb3a432268e5831000000000000000000000000000000" +
			"000000000000000000000000000003c1e50000000000000000000000" +
			"00000000000000000000000000000000000000000000000000000000" +
			"000000000000000000000000000000000000000000000000a0000000" +
			"00000000000000000000000000000000000000000000000000000000" +
			"148c89ed219d02f1a5be012c689b4f5b731827bebe00000000000000" +
			"0000000000c001a033fd89cb37c31b2cba46b6466e040c61fc9b2a36" +
			"75a7f5f493ebd5ad77c497f8a07cdf65680e238392693019b4092f61" +
			"0222e71b7cec06449cb922b93b6a12744e"
		contractCallTx, _ = hex.DecodeString(contractCallTxStr)
	)

	testCases := []struct {
		input       []byte
		expectedLen uint32
	}{
		// empty input
		{[]byte{}, 0},
		// all 1 inputs
		{bytes.Repeat([]byte{1}, 1000), 21},
		// all 0 inputs
		{make([]byte, 1000), 21},
		// empty tx input
		{emptyTxBytes, 31},
		// contract call tx: https://optimistic.etherscan.io/tx/0x8eb9dd4eb6d33f4dc25fb015919e4b1e9f7542f9b0322bf6622e268cd116b594
		{contractCallTx, 202},
	}

	for _, tc := range testCases {
		output := FlzCompressLen(tc.input)
		require.Equal(t, tc.expectedLen, output)
	}
}
