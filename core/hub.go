package main

import (
	"context"
	"core/state"
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/provider"
	"github.com/metacubex/mihomo/adapter/outboundgroup"
	"github.com/metacubex/mihomo/common/observable"
	"github.com/metacubex/mihomo/common/utils"
	"github.com/metacubex/mihomo/component/mmdb"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/component/updater"
	"github.com/metacubex/mihomo/config"
	"github.com/metacubex/mihomo/constant"
	cp "github.com/metacubex/mihomo/constant/provider"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/log"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

var (
	isInit            = false
	externalProviders = map[string]cp.Provider{}
	logSubscriber     observable.Subscription[log.Event]
	healthCheckStopCh chan struct{}
	healthCheckSeen   = map[string]string{}
)

func handleInitClash(paramsString string) bool {
	var params = InitParams{}
	err := json.Unmarshal([]byte(paramsString), &params)
	if err != nil {
		return false
	}
	version = params.Version
	constant.SetHomeDir(params.HomeDir)
	if !isInit {
		isInit = true
	}
	return isInit
}

func handleStartListener() bool {
	runLock.Lock()
	defer runLock.Unlock()
	if currentConfig != nil {
		currentConfig.General.Tun.Enable = pendingTunEnable
		// Apply full runtime config only when user explicitly starts.
		executor.ApplyConfig(currentConfig, true)
	}
	isRunning = true
	updateListeners()
	resolver.ResetConnection()
	startHealthCheckForwarder()
	return true
}

func handleStopListener() bool {
	runLock.Lock()
	defer runLock.Unlock()
	isRunning = false
	stopHealthCheckForwarder()
	stopListeners()
	return true
}

func handleGetIsInit() bool {
	return isInit
}

func handleForceGc() {
	go func() {
		log.Infoln("[APP] request force GC")
		runtime.GC()
	}()
}

func handleShutdown() bool {
	stopHealthCheckForwarder()
	stopListeners()
	executor.Shutdown()
	runtime.GC()
	isInit = false
	return true
}

func startHealthCheckForwarder() {
	if healthCheckStopCh != nil {
		return
	}
	healthCheckStopCh = make(chan struct{})
	go func(stopCh chan struct{}) {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				forwardHealthCheckDelays()
			case <-stopCh:
				return
			}
		}
	}(healthCheckStopCh)
}

func stopHealthCheckForwarder() {
	if healthCheckStopCh == nil {
		return
	}
	close(healthCheckStopCh)
	healthCheckStopCh = nil
}

func resetHealthCheckForwarderState() {
	healthCheckSeen = map[string]string{}
}

func forwardHealthCheckDelays() {
	runLock.Lock()
	if !isRunning {
		runLock.Unlock()
		return
	}
	triggerProviderHealthChecks()
	proxies := proxiesWithProviders()
	runLock.Unlock()

	for name, proxy := range proxies {
		emitLatestDelay(name, "", proxy.DelayHistory())
		for url, state := range proxy.ExtraDelayHistories() {
			emitLatestDelay(name, url, state.History)
		}
	}
}

func triggerProviderHealthChecks() {
	for _, p := range tunnel.Providers() {
		pp, ok := p.(*provider.ProxySetProvider)
		if !ok {
			continue
		}
		pp.HealthCheck()
	}
}

func emitLatestDelay(proxyName string, testURL string, history []constant.DelayHistory) {
	if len(history) == 0 {
		return
	}
	latest := history[len(history)-1]
	key := proxyName + "|" + testURL
	signature := fmt.Sprintf("%d:%d", latest.Time.UnixNano(), latest.Delay)
	if healthCheckSeen[key] == signature {
		return
	}
	healthCheckSeen[key] = signature

	delayValue := int32(latest.Delay)
	if latest.Delay == 0 {
		delayValue = -1
	}
	sendMessage(Message{
		Type: DelayMessage,
		Data: &Delay{
			Url:   testURL,
			Name:  proxyName,
			Value: delayValue,
		},
	})
}

func handleValidateConfig(bytes []byte) string {
	_, err := config.UnmarshalRawConfig(bytes)
	if err != nil {
		return err.Error()
	}
	return ""
}

func handleGetProxies() interface{} {
	runLock.Lock()
	defer runLock.Unlock()
	return proxiesWithDescriptions()
}

func handleChangeProxy(data string, fn func(string string)) {
	runLock.Lock()
	go func() {
		defer runLock.Unlock()
		var params = &ChangeProxyParams{}
		err := json.Unmarshal([]byte(data), params)
		if err != nil {
			fn(err.Error())
			return
		}
		groupName := *params.GroupName
		proxyName := *params.ProxyName
		proxies := proxiesWithProviders()
		group, ok := proxies[groupName]
		if !ok {
			fn("Not found group")
			return
		}
		adapterProxy := group.(*adapter.Proxy)
		selector, ok := adapterProxy.ProxyAdapter.(outboundgroup.SelectAble)
		if !ok {
			fn("Group is not selectable")
			return
		}
		if proxyName == "" {
			selector.ForceSet(proxyName)
		} else {
			err = selector.Set(proxyName)
		}
		if err != nil {
			fn(err.Error())
			return
		}

		fn("")
		return
	}()
}

func handleGetTraffic() string {
	up, down := statistic.DefaultManager.Now()
	traffic := map[string]int64{
		"up":   up,
		"down": down,
	}
	data, err := json.Marshal(traffic)
	if err != nil {
		fmt.Println("Error:", err)
		return ""
	}
	return string(data)
}

func handleGetTotalTraffic() string {
	up, down := statistic.DefaultManager.Total()
	traffic := map[string]int64{
		"up":   up,
		"down": down,
	}
	data, err := json.Marshal(traffic)
	if err != nil {
		fmt.Println("Error:", err)
		return ""
	}
	return string(data)
}

func handleResetTraffic() {
	statistic.DefaultManager.ResetStatistic()
}

func handleAsyncTestDelay(paramsString string, fn func(string)) {
	mBatch.Go(paramsString, func() (bool, error) {
		var params = &TestDelayParams{}
		err := json.Unmarshal([]byte(paramsString), params)
		if err != nil {
			fn("")
			return false, nil
		}

		expectedStatus, err := utils.NewUnsignedRanges[uint16]("")
		if err != nil {
			fn("")
			return false, nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*time.Duration(params.Timeout))
		defer cancel()

		proxies := proxiesWithProviders()
		proxy := proxies[params.ProxyName]

		delayData := &Delay{
			Name: params.ProxyName,
		}

		if proxy == nil {
			delayData.Value = -1
			data, _ := json.Marshal(delayData)
			fn(string(data))
			return false, nil
		}

		testUrl := "https://www.gstatic.com/generate_204"

		if params.TestUrl != "" {
			testUrl = params.TestUrl
		}
		delayData.Url = testUrl

		delay, err := proxy.URLTest(ctx, testUrl, expectedStatus)
		if err != nil || delay == 0 {
			delayData.Value = -1
			data, _ := json.Marshal(delayData)
			fn(string(data))
			return false, nil
		}

		delayData.Value = int32(delay)
		data, _ := json.Marshal(delayData)
		fn(string(data))

		// Push delay update via message
		sendMessage(Message{
			Type: DelayMessage,
			Data: delayData,
		})

		return false, nil
	})
}

func handleGetConnections() string {
	runLock.Lock()
	defer runLock.Unlock()
	snapshot := statistic.DefaultManager.Snapshot()
	data, err := json.Marshal(snapshot)
	if err != nil {
		fmt.Println("Error:", err)
		return ""
	}
	return string(data)
}

func handleCloseConnections() bool {
	runLock.Lock()
	defer runLock.Unlock()
	closeConnections()
	return true
}

func closeConnections() {
	statistic.DefaultManager.Range(func(c statistic.Tracker) bool {
		err := c.Close()
		if err != nil {
			return false
		}
		return true
	})
}

func handleResetConnections() bool {
	runLock.Lock()
	defer runLock.Unlock()
	resolver.ResetConnection()
	return true
}

func handleCloseConnection(connectionId string) bool {
	runLock.Lock()
	defer runLock.Unlock()
	c := statistic.DefaultManager.Get(connectionId)
	if c == nil {
		return false
	}
	_ = c.Close()
	return true
}

func handleGetExternalProviders() string {
	runLock.Lock()
	defer runLock.Unlock()
	externalProviders = getExternalProvidersRaw()
	eps := make([]ExternalProvider, 0)
	for _, p := range externalProviders {
		externalProvider, err := toExternalProvider(p)
		if err != nil {
			continue
		}
		eps = append(eps, *externalProvider)
	}
	sort.Sort(ExternalProviders(eps))
	data, err := json.Marshal(eps)
	if err != nil {
		return ""
	}
	return string(data)
}

func handleGetExternalProvider(externalProviderName string) string {
	runLock.Lock()
	defer runLock.Unlock()
	externalProvider, exist := externalProviders[externalProviderName]
	if !exist {
		return ""
	}
	e, err := toExternalProvider(externalProvider)
	if err != nil {
		return ""
	}
	data, err := json.Marshal(e)
	if err != nil {
		return ""
	}
	return string(data)
}

func handleUpdateGeoData(geoType string, geoName string, fn func(value string)) {
	go func() {
		var err error
		switch geoType {
		case "MMDB":
			err = updater.UpdateMMDB()
		case "ASN":
			err = updater.UpdateASN()
		case "GeoIp":
			err = updater.UpdateGeoIp()
		case "GeoSite":
			err = updater.UpdateGeoSite()
		}
		if err != nil {
			fn(err.Error())
			return
		}
		fn("")
	}()
}

func handleUpdateExternalProvider(providerName string, fn func(value string)) {
	go func() {
		externalProvider, exist := externalProviders[providerName]
		if !exist {
			fn("external provider is not exist")
			return
		}
		err := externalProvider.Update()
		if err != nil {
			fn(err.Error())
			return
		}
		fn("")
	}()
}

func handleSideLoadExternalProvider(providerName string, data []byte, fn func(value string)) {
	go func() {
		runLock.Lock()
		defer runLock.Unlock()
		externalProvider, exist := externalProviders[providerName]
		if !exist {
			fn("external provider is not exist")
			return
		}
		err := sideUpdateExternalProvider(externalProvider, data)
		if err != nil {
			fn(err.Error())
			return
		}
		fn("")
	}()
}

func handleStartLog() {
	if logSubscriber != nil {
		log.UnSubscribe(logSubscriber)
		logSubscriber = nil
	}
	logSubscriber = log.Subscribe()
	go func() {
		for logData := range logSubscriber {
			if logData.LogLevel < log.Level() {
				continue
			}
			message := &Message{
				Type: LogMessage,
				Data: logData,
			}
			sendMessage(*message)
		}
	}()
}

func handleStopLog() {
	if logSubscriber != nil {
		log.UnSubscribe(logSubscriber)
		logSubscriber = nil
	}
}

func handleGetCountryCode(ip string, fn func(value string)) {
	go func() {
		runLock.Lock()
		defer runLock.Unlock()
		codes := mmdb.IPInstance().LookupCode(net.ParseIP(ip))
		if len(codes) == 0 {
			fn("")
			return
		}
		fn(codes[0])
	}()
}

func handleGetMemory(fn func(value string)) {
	go func() {
		fn(strconv.FormatUint(statistic.DefaultManager.Memory(), 10))
	}()
}

func handleSetState(params string) {
	_ = json.Unmarshal([]byte(params), state.CurrentState)
}

func handleGetConfig(path string) (*config.RawConfig, error) {
	bytes, err := readFile(path)
	if err != nil {
		return nil, err
	}
	prof, err := config.UnmarshalRawConfig(bytes)
	if err != nil {
		return nil, err
	}
	return prof, nil
}

func handleCrash() {
	panic("handle invoke crash")
}

func handleUpdateConfig(bytes []byte) string {
	var params = &UpdateParams{}
	err := json.Unmarshal(bytes, params)
	if err != nil {
		return err.Error()
	}
	updateConfig(params)
	return ""
}

func handleSetupConfig(bytes []byte) string {
	var params = defaultSetupParams()
	err := UnmarshalJson(bytes, params)
	if err != nil {
		log.Errorln("unmarshalRawConfig error %v", err)
		_ = setupConfig(defaultSetupParams())
		return err.Error()
	}
	err = setupConfig(params)
	if err != nil {
		return err.Error()
	}
	return ""
}
