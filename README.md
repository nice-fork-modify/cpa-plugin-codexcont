# cpa-plugin-codexcont

用于 CLIProxyAPI 的 CodexCont 折叠推理动态库插件

## 运行要求

- CLIProxyAPI 插件 ABI，依赖 `github.com/router-for-me/CLIProxyAPI/v7 v7.2.50`
- 输入协议：`responses`、`codex`
- 输出协议：`responses`
- Linux 构建基于 `golang:1.26-bookworm` 和 glibc，不支持禁用动态库插件的 musl/Alpine 版本

## 安装

插件文件名必须为 `codexcont` 加对应平台扩展名，并放入 CLIProxyAPI 插件目录。

```text
plugins/
├── darwin/
│   └── arm64/
│       └── codexcont.dylib
├── linux/
│   ├── amd64/
│   │   └── codexcont.so
│   └── arm64/
│       └── codexcont.so
└── windows/
    └── amd64/
        └── codexcont.dll
```

## 配置

通用配置页面注册 `model_patterns` 和 `max_continue`：多个模型使用英文逗号分隔，普通模型名会自动追加 `*`；最大续写轮数默认 `3`。其他高级字段仍可直接写入 YAML。

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    codexcont:
      enabled: true
      priority: 50
      source_formats:
        - responses
        - codex
      exit_protocol: responses
      model_patterns: "gpt-5.5,gpt-5.6-sol"
      truncation_step: 518
      max_continue: 3
      min_n: 1
      max_n: 6
      marker_text: "Continue thinking. Do not repeat prior final answer; continue from the hidden reasoning state."
      forward_marker: false
      force_include_encrypted: true
      rechunk_final_answer: true
      rechunk_size: 8
      max_total_output_tokens: 0
```

推荐模型选择：

```text
gpt-5.3       -> gpt-5.3*
gpt-5.4       -> gpt-5.4*
gpt-5.5       -> gpt-5.5*
gpt-5.6-sol   -> gpt-5.6-sol*
gpt-5.6-terra -> gpt-5.6-terra*
gpt-5.6-luna  -> gpt-5.6-luna*
```

状态页面只读展示当前模型选择；修改配置需使用 CLIProxyAPI 提供的已认证插件配置表单。

## 状态页面

CLIProxyAPI 启用插件管理路由后，CodexCont 提供以下页面和接口。

```text
/v0/resource/plugins/codexcont/status      # 只读状态页面
/v0/resource/plugins/codexcont/stats.json  # 未认证非敏感聚合数据
/v0/management/plugins/codexcont/stats     # 已认证聚合数据
```

Resource 路由不要求管理认证，只展示运行总量和安全的有效配置子集，不包含请求 ID、请求时间明细、提示词、响应正文、认证数据、加密推理、续写标记或错误正文。

统计仅保存在当前插件进程内，CLIProxyAPI 或插件进程重启后清零，`plugin.reconfigure` 不会清零。

统计内容包括：

- 当前活动、已接管、续写、完成和失败请求数
- 续写总轮数和停止原因
- 按模型聚合的接管、续写、完成和失败数量
- reasoning、output 和 billed token 总量

只有折叠执行器实际接管的请求才计入统计；路由判断和透传请求不计入。

## 构建

在目标平台构建可减少动态库和 C 运行库兼容问题。

```bash
# 运行测试
make test

# 构建当前系统和架构的动态库
make build-native

# 构建到 CLIProxyAPI 插件目录结构
make build-plugin-tree

# 通过容器构建 Linux amd64 和 arm64 动态库
make build-linux-amd64-container
make build-linux-arm64-container
```

容器构建脚本依次检测 `docker`、`podman` 和 Apple `container`；构建产物位于 `dist/<GOOS>/<GOARCH>`。

## 发布产物

版本标签触发 GitHub Actions 测试和构建，Release 资产使用以下命名。

```text
codexcont_0.1.4_darwin_arm64.zip
codexcont_0.1.4_linux_amd64.zip
codexcont_0.1.4_linux_arm64.zip
checksums.txt
```

## 行为限制

- 仅处理启用 reasoning、使用流式响应并匹配 `source_formats` 和 `model_patterns` 的请求
- `exit_protocol` 仅支持 `responses`，其他值回退为 `responses`
- `max_total_output_tokens` 为 `0` 时不限制累计 billed output token
- 上游未返回 usage 时不估算 token
- 统计不持久化，也不跨 CLIProxyAPI 实例聚合

## 来源

折叠推理思路和截断判定参考 [neteroster/CodexCont](https://github.com/neteroster/CodexCont)，插件通过 CLIProxyAPI 官方插件 ABI 执行模型调用和流式输出。
