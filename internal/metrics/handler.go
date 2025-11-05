package metrics

// RegisterMetricsEndpoint 注册指标端点到指定的路由器 (简化版本)
// 由于移除了prometheus，此函数不再需要注册/metrics端点
func RegisterMetricsEndpoint(mux interface{}, path string) {
	// 移除prometheus支持，不再注册metrics端点
}
