package settlement

import (
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestValidatorControlLogCannotForgeForeignOperatorOrEmitter(t *testing.T) {
	master := common.HexToAddress("0x7001")
	operator := common.HexToAddress("0x7002")
	compiled, err := contracts.CompiledABI("NewNetworkMasterValidator")
	require.NoError(t, err)
	topic := compiled.Events["ValidatorUpdated"].ID
	data := append(smallWord(1).Bytes(), smallWord(0).Bytes()...)
	valid := &ethtypes.Log{Address: master, Topics: []common.Hash{topic, common.BytesToHash(operator.Bytes())}, Data: data}
	require.NoError(t, exactValidatorControlLog(valid, master, operator, topic, data))
	for _, change := range []func(*ethtypes.Log){
		func(l *ethtypes.Log) { l.Address = operator },
		func(l *ethtypes.Log) { l.Topics[1] = common.BytesToHash(master.Bytes()) },
		func(l *ethtypes.Log) { l.Topics[0] = common.Hash{} },
		func(l *ethtypes.Log) { l.Topics = append(l.Topics, common.Hash{}) },
		func(l *ethtypes.Log) { l.Data = l.Data[:32] },
		func(l *ethtypes.Log) { l.Removed = true },
	} {
		copy := *valid
		copy.Topics = append([]common.Hash(nil), valid.Topics...)
		copy.Data = append([]byte(nil), valid.Data...)
		change(&copy)
		require.Error(t, exactValidatorControlLog(&copy, master, operator, topic, data))
	}
	require.Error(t, exactValidatorControlLog(nil, master, operator, topic, data))
}
