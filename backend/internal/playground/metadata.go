package playground

import sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

// PluginVersion defaults to the local development version.
// Release builds override it with the git tag via ldflags.
var PluginVersion = "dev"

func BuildPluginInfo() sdk.PluginInfo {
	return sdk.PluginInfo{
		ID:          "airgate-playground",
		Name:        "AI Chat",
		Version:     PluginVersion,
		SDKVersion:  sdk.SDKVersion,
		Description: "Web-based AI chat playground with multi-model support and conversation management",
		Author:      "HopBase",
		Type:        sdk.PluginTypeExtension,
		ConfigSchema: []sdk.ConfigField{
			{
				Key:         "max_conversations_per_user",
				Label:       "每用户最多会话数",
				Type:        "int",
				Default:     "10",
				Description: "达到上限后，用户需要先删除旧会话，才能创建新会话。填写 0 表示不限制。",
				Placeholder: "10",
			},
			{
				Key:         "chat_system_prompt",
				Label:       "补充 System Prompt（可选）",
				Type:        "textarea",
				Default:     "",
				Description: "追加在内置基础规则之后，适合填写 ToB / ToC 共用的品牌语气与业务边界；留空即只使用内置规则。不要写死单一站点品牌。注意：所路由分组的账号不能开启 claude_code_only（其 system 校验会拒绝自定义 prompt）。",
				Placeholder: "示例：\n你服务同时使用 ToB 与 ToC 产品的用户。\n- 保持专业、友好、简洁\n- 涉及价格、配额、政策或服务状态时，不确定就明确说明，不要猜测\n- 不承诺未公开的功能、时效或服务等级",
			},
			{
				Key:         "claude_prompt_cache",
				Label:       "Claude Prompt Caching",
				Type:        "bool",
				Default:     "true",
				Description: "多轮会话对 Claude 注入 cache_control 断点，历史前缀按缓存读价计费（约 0.1×）。上游不支持时会自动降级重试，本开关仅作应急兜底。",
			},
			{
				Key:         "chat_default_max_tokens",
				Label:       "默认 max_tokens 上限",
				Type:        "int",
				Default:     "32768",
				Description: "Claude 非思考请求的 max_tokens 上限（会再按模型 max_output 收紧）。计费按实际输出 tokens。",
				Placeholder: "32768",
			},
			{
				Key:         "web_search_enabled",
				Label:       "Web 搜索工具",
				Type:        "bool",
				Default:     "false",
				Description: "开启后模型可经服务端工具循环执行网页搜索（需同时配置 Tavily API Key，且前端为支持工具渲染的版本）。",
			},
			{
				Key:         "tavily_api_key",
				Label:       "Tavily API Key",
				Type:        "password",
				Default:     "",
				Description: "web_search 工具的搜索提供商密钥（tavily.com，免费额度 1000 次/月）。留空则搜索工具不注册。",
			},
			{
				Key:         "tool_loop_max_iterations",
				Label:       "工具循环最大迭代数",
				Type:        "int",
				Default:     "5",
				Description: "单条消息内模型↔工具往返的上限，触达后强制模型给出文本终答。",
				Placeholder: "5",
			},
			{
				Key:         "max_searches_per_message",
				Label:       "每消息搜索次数上限",
				Type:        "int",
				Default:     "3",
				Description: "单条消息内 web_search 的执行次数上限。",
				Placeholder: "3",
			},
			{
				Key:         "generate_document_enabled",
				Label:       "文档生成工具",
				Type:        "bool",
				Default:     "false",
				Description: "开启后模型可生成真实 Markdown/PDF 文档交付给用户。PDF 需要 chromium 边车；不可达时会明确报错，不会用 Markdown 冒充 PDF。",
			},
			{
				Key:         "generate_spreadsheet_enabled",
				Label:       "Excel 表格生成工具",
				Type:        "bool",
				Default:     "false",
				Description: "开启后模型可通过受控结构生成 XLSX 文件。使用 Excelize 本地渲染，不额外消耗模型 token；首版渲染服务费默认为 0。",
			},
			{
				Key:         "generate_office_enabled",
				Label:       "Word / PPT 生成工具",
				Type:        "bool",
				Default:     "false",
				Description: "开启后模型可生成真实 DOCX 与 PPTX 文件。需要内部 office-renderer 边车；渲染本身不额外消耗模型 token。",
			},
			{
				Key:         "office_renderer_url",
				Label:       "Office renderer 地址",
				Type:        "string",
				Default:     "http://office-renderer:8787",
				Description: "DOCX/PPTX 内部渲染服务地址，不应暴露公网。",
				Placeholder: "http://office-renderer:8787",
			},
			{
				Key:         "chromium_cdp_url",
				Label:       "Chromium 边车 CDP 地址",
				Type:        "string",
				Default:     "http://chromium:9222",
				Description: "headless-shell 边车的 DevTools 端点（compose 服务名+端口，须在 internal 网络内）。",
				Placeholder: "http://chromium:9222",
			},
			{
				Key:         "pdf_render_fee",
				Label:       "PDF 渲染费",
				Type:        "float",
				Default:     "0",
				Description: "每个成功生成的 PDF 文件费用；0 表示免费灰度。该费用独立于模型 token 计费。",
			},
			{
				Key:         "docx_render_fee",
				Label:       "Word 渲染费",
				Type:        "float",
				Default:     "0",
				Description: "每个成功生成的 DOCX 文件费用；0 表示免费灰度。",
			},
			{
				Key:         "pptx_render_fee",
				Label:       "PowerPoint 渲染费",
				Type:        "float",
				Default:     "0",
				Description: "每个成功生成的 PPTX 文件费用；0 表示免费灰度。",
			},
			{
				Key:         "xlsx_render_fee",
				Label:       "Excel 渲染费",
				Type:        "float",
				Default:     "0",
				Description: "每个成功生成的 XLSX 文件费用；0 表示免费灰度。",
			},
		},
		Capabilities: []sdk.Capability{
			sdk.CapabilityForHostMethod(hostMethodGatewayForward),
			sdk.CapabilityForHostMethod(hostMethodUsersGet),
			sdk.CapabilityForHostMethod(hostMethodModelsList),
			// 成员分组资格判定（/models 裁剪与白名单拒绝识别）经 groups.list；未声明会被 core 拒为 PermissionDenied，
			// 插件端查询失败即放行，模型列表就退化成全量——2026-09-09 生产 E2E 抓到的漏洞。
			sdk.CapabilityForHostMethod(hostMethodGroupsList),
			sdk.CapabilityForHostMethod(hostMethodAssetsStore),
			sdk.CapabilityForHostMethod(hostMethodAssetsGetURL),
			sdk.CapabilityForHostMethod(hostMethodAssetsGetBytes),
			sdk.CapabilityForHostMethod(hostMethodAssetsDelete),
			sdk.CapabilityForHostMethod(hostMethodUsageRecord),
		},
		FrontendPages: []sdk.FrontendPage{
			{
				Path:        "/playground",
				Title:       "playground.title",
				Icon:        "message-square",
				Description: "AI chat playground",
				Audience:    "all",
			},
		},
	}
}
