package monitor

import (
	"sync"

	"github.com/songquanpeng/one-api/common/config"
)

var store = make(map[int][]bool)
var storeMu sync.Mutex // dyt-53: store 由两个 consumer goroutine 并发访问，需要加锁
var metricSuccessChan = make(chan int, config.MetricSuccessChanSize)
var metricFailChan = make(chan int, config.MetricFailChanSize)

func consumeSuccess(channelId int) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if len(store[channelId]) > config.MetricQueueSize {
		store[channelId] = store[channelId][1:]
	}
	store[channelId] = append(store[channelId], true)
}

func consumeFail(channelId int) (bool, float64) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if len(store[channelId]) > config.MetricQueueSize {
		store[channelId] = store[channelId][1:]
	}
	store[channelId] = append(store[channelId], false)
	successCount := 0
	for _, success := range store[channelId] {
		if success {
			successCount++
		}
	}
	successRate := float64(successCount) / float64(len(store[channelId]))
	if len(store[channelId]) < config.MetricQueueSize {
		return false, successRate
	}
	if successRate < config.MetricSuccessRateThreshold {
		store[channelId] = make([]bool, 0)
		return true, successRate
	}
	return false, successRate
}

func metricSuccessConsumer() {
	for {
		select {
		case channelId := <-metricSuccessChan:
			consumeSuccess(channelId)
		}
	}
}

func metricFailConsumer() {
	for {
		select {
		case channelId := <-metricFailChan:
			disable, successRate := consumeFail(channelId)
			if disable {
				go MetricDisableChannel(channelId, successRate)
			}
		}
	}
}

func init() {
	if config.EnableMetric {
		go metricSuccessConsumer()
		go metricFailConsumer()
	}
}

func Emit(channelId int, success bool) {
	if !config.EnableMetric {
		return
	}
	// 非阻塞发送：channel 满时丢弃，避免 goroutine 堆积
	if success {
		select {
		case metricSuccessChan <- channelId:
		default:
		}
	} else {
		select {
		case metricFailChan <- channelId:
		default:
		}
	}
}
