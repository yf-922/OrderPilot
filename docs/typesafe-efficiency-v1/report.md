# 官方 Jev 与上下文压缩：三组同源效率验证

完成状态：true。三组使用相同两条 8 轮场景（普通跨轮指代、早期禁止操作约束）、同一主模型与相同 Mock 业务工具，串行按轮次循环交错。该实验是实际 Provider 自由运行 E2E 小样本，各组生成的历史可能不同；不是 Teacher Forcing，也不是生产用户流量。

C=三级压缩无路由；R=三级压缩+确定性规则；J=三级压缩+规则+官方 Jev。三组压缩配置相同，R→J 衡量外部模型的增量。所有用量来自 Provider 返回；Jev 自身消耗计入合计输入及输出。

|组|轮数|主模型请求|Jev 请求/采用|主输入/输出|Jev 输入/输出|合计输入|合计输出|平均每轮输入|主模型缓存输入|均值ms|P50ms|P95ms|Context均值ms|主网络均值ms|Jev均值ms|严格轨迹正确轮|指代正确轮|最终任务完成|工具调用成功|API错误|
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|---|---:|
|C_compressed_off|16|30|0/0|77431/1494|0/0|77431|1494|4839.4|2048|5633.2|5370.0|12261.0|10.06|5599.5|0.0|16/16|4/4|2/2|16/16|0|
|R_compressed_rules|16|29|0/0|65847/1539|0/0|65847|1539|4115.4|0|7301.1|6506.0|18893.0|10.75|7266.8|0.0|15/16|4/4|2/2|17/17|0|
|J_compressed_typesafe|16|28|5/2|64745/1385|5853/1155|70598|2540|4412.4|0|9322.4|8740.0|28820.0|9.31|8971.6|318.4|16/16|4/4|2/2|16/16|0|

## 逐轮结果

|场景|组|轮|合计输入|合计输出|总耗时ms|Context ms|主网络ms|Jev ms|主请求数|路由来源|严格参数正确|
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|---|
|S1|C_compressed_off|1|1644|125|5376|3|5365|0|2||true|
|S1|J_compressed_typesafe|1|2506|326|5966|5|4734|1203|2|fallback|true|
|S1|R_compressed_rules|1|2163|244|8832|7|8807|0|3|rules|false|
|S1|C_compressed_off|2|3984|120|5370|13|5336|0|2||true|
|S1|J_compressed_typesafe|2|4834|341|16600|8|16287|286|2|typesafe|true|
|S1|R_compressed_rules|2|3752|128|9928|20|9894|0|2|rules|true|
|S1|C_compressed_off|3|6098|84|4175|14|4142|0|2||true|
|S1|J_compressed_typesafe|3|5672|67|9616|11|9583|0|2|rules|true|
|S1|R_compressed_rules|3|5802|52|8219|12|8180|0|2|rules|true|
|S1|C_compressed_off|4|5280|120|6006|8|5972|0|2||true|
|S1|J_compressed_typesafe|4|4772|120|5915|14|5882|0|2|rules|true|
|S1|R_compressed_rules|4|4894|120|4862|9|4831|0|2|rules|true|
|S1|C_compressed_off|5|5702|60|5476|9|5445|0|2||true|
|S1|J_compressed_typesafe|5|5223|71|28820|12|28785|0|2|rules|true|
|S1|R_compressed_rules|5|5345|72|10336|29|10286|0|2|rules|true|
|S1|C_compressed_off|6|5646|51|4584|14|4550|0|2||true|
|S1|J_compressed_typesafe|6|6811|297|5389|5|4184|1180|2|fallback|true|
|S1|R_compressed_rules|6|5466|79|11043|13|11007|0|2|rules|true|
|S1|C_compressed_off|7|5775|118|7881|21|7835|0|2||true|
|S1|J_compressed_typesafe|7|5303|80|11741|19|11693|0|2|rules|true|
|S1|R_compressed_rules|7|5289|102|6989|12|6952|0|2|rules|true|
|S1|C_compressed_off|8|3592|21|1989|5|1974|0|1||true|
|S1|J_compressed_typesafe|8|0|0|0|0|0|0|0|rules|true|
|S1|R_compressed_rules|8|0|0|1|0|0|0|0|rules|true|
|S3|C_compressed_off|1|1826|227|12261|11|12229|0|2||true|
|S3|J_compressed_typesafe|1|1514|236|8740|13|8697|0|2|rules|true|
|S3|R_compressed_rules|1|1514|191|18893|6|18866|0|2|rules|true|
|S3|C_compressed_off|2|4194|129|6483|8|6436|0|2||true|
|S3|J_compressed_typesafe|2|5022|341|9811|6|8585|1192|2|typesafe|true|
|S3|R_compressed_rules|2|3792|124|6506|9|6470|0|2|rules|true|
|S3|C_compressed_off|3|6326|87|4842|11|4795|0|2||true|
|S3|J_compressed_typesafe|3|5822|79|4484|9|4446|0|2|rules|true|
|S3|R_compressed_rules|3|5914|83|5966|14|5923|0|2|rules|true|
|S3|C_compressed_off|4|5522|86|4754|10|4711|0|2||true|
|S3|J_compressed_typesafe|4|4976|138|10697|8|10659|0|2|rules|true|
|S3|R_compressed_rules|4|5076|114|4746|15|4694|0|2|rules|true|
|S3|C_compressed_off|5|5884|60|5847|14|5805|0|2||true|
|S3|J_compressed_typesafe|5|5435|66|14668|17|14617|0|2|rules|true|
|S3|R_compressed_rules|5|5467|64|5420|10|5373|0|2|rules|true|
|S3|C_compressed_off|6|5893|91|7972|7|7935|0|2||true|
|S3|J_compressed_typesafe|6|7085|306|7286|11|6006|1233|2|fallback|true|
|S3|R_compressed_rules|6|5661|78|4534|11|4478|0|2|rules|true|
|S3|C_compressed_off|7|6095|94|4829|9|4794|0|2||true|
|S3|J_compressed_typesafe|7|5623|72|9425|11|9388|0|2|rules|true|
|S3|R_compressed_rules|7|5712|88|10543|5|10507|0|2|rules|true|
|S3|C_compressed_off|8|3970|21|2286|4|2268|0|1||true|
|S3|J_compressed_typesafe|8|0|0|0|0|0|0|0|rules|true|
|S3|R_compressed_rules|8|0|0|0|0|0|0|0|rules|true|

## 同源增量比较

|比较|合计输入下降|合计输入输出下降|平均 E2E 耗时下降|
|---|---:|---:|---:|
|C_compressed_off → R_compressed_rules|14.96%|14.62%|-29.61%|
|R_compressed_rules → J_compressed_typesafe|-7.22%|-8.54%|-27.68%|
|C_compressed_off → J_compressed_typesafe|8.82%|7.33%|-65.49%|

预算占用（已返回用量+未知请求保守预留）：219449/500000 Token；含此前预留累计 2028015/10000000。负降幅表示增加。

指标定义：任务完成率=最终只取消正确订单且未提前取消的成功场景数/场景数；工具调用成功率=后端成功返回的工具执行数/全部工具执行数，不等于工具选择或参数提取准确率，严格轨迹另列。输入按所有主模型调用与官方 Jev 的实际输入合计，平均输入按用户轮次归一化，不因二次确认绕过而改变分母。

结论边界：每组仅两条对话、16 个用户轮次，P95 只作描述，不能用于线上 SLA 或宣称统计显著；准确率请保留分子/分母。工具与 RAG 是固定本地 fixture，不代表生产数据库与检索延迟。缓存返回量可观察但未做冷热缓存随机试验，不能将延迟差归因于缓存。Jev 美元费用可按官方输入单价估算，主模型网关价格未知，因此不虚构总美元降幅。失败与回退不计作外部决策成功。
