# 复制给 AI：第一次启动

请作为我的享玩项目工程师，实际执行以下工作。我不是程序员，请自行完成命令、排错和必要的可逆修复，用普通中文汇报。先完成项目准备，再开始我的功能需求。

项目仓库：https://github.com/wzyhn/xiangwanai 。我的交接包解压目录由我在本条提示词后提供；先读取其中“先读我.txt”、交接说明和授权范围。真实 MCP 文件位于解压目录 private/mcp.json；原发送者电脑的路径是 C:\Users\w'w'z\Downloads\mcp.json，接收电脑不能依赖这个原路径。

1. 查看本地已有目录与 Git 状态，保护未提交工作；在独立目录工作。使用我的 GitHub 登录身份，核实 gh auth status，只显示登录账号，不显示 token。没有登录就引导我完成 gh auth login 的浏览器授权。不要使用其他人的 GitHub 身份、私有平台凭据或已有项目目录。
2. 我可以修改整个享玩项目。若我有 wzyhn/xiangwanai 的 write 权限，克隆上游并在 codex/ 前缀分支修改；若没有，使用 gh repo fork wzyhn/xiangwanai --clone，在我的 Fork 工作，核实 origin 指向我的 Fork、upstream 指向 wzyhn/xiangwanai。若已克隆，先核实远程地址，再添加或使用 upstream。拉取 upstream main，以它创建自己的工作分支。没有 gh 时可用 GitHub 网页 Fork + git clone；不要因为工具缺失停止整个任务。
3. 阅读 AGENTS.md、README.md、CONTRIBUTING.md、SECURITY.md，以及目标 app 的 README 和 docs/handoff。工程要求 Go 1.25.13、Node.js 24、pnpm 10.8.0。检查版本，按官方方式安装缺失工具，不改变我电脑上其他项目的配置。Go 自动工具链必要时只在当前进程设置 GOTOOLCHAIN=auto。
4. 在仓库根执行 pnpm install --frozen-lockfile、pnpm test、go test ./...、go build ./cmd/xiangwan ./cmd/sqlmigrate。修改后台时还要设置 XIANGWAN_ADMIN_API_ORIGIN=http://localhost:8080 再运行 lint/typecheck/build。记录原有失败与修改后失败，不把没有跑过的检查记为通过。
5. 小程序导入 apps/miniprogram-xiangwan；引导我用自己的微信开发者账号登录。如果没有该 AppID 的开发权限，明确告诉我需要所有者在微信公众平台添加开发者。每次“构建 npm”后在仓库根依次运行两个 sync-miniprogram 脚本，见 README。release.json 和 release.js 必须保持一致；AppSecret 只在服务端。
6. 默认 API 是现有线上 https://api.weconq.cn。准备隔离的本地或测试环境；测试使用自己的记录，不通过真实付款、退款或批量修改生产数据来证明代码正确。公开源码不包含生产环境文件，服务器已有配置通过我授权的 MCP 核对。
7. 从本地文件加载 Coolify MCP。若 AI 客户端需要自己的配置格式，将 mcp.json 中 url、headers 在本机转换配置，不输出 Authorization。连接后先查看工具目录和当前团队，读取现有享玩资源。交接授权允许修改、构建和部署整个享玩；权限与实际操作范围以私下授权说明为准。初次启动只盘点，功能发布按部署提示词完成。
8. 给我一个简短结果：当前登录账号、工作目录/分支、可打开的本地页面、通过的检查、真实存在的阻碍。不要泛泛要求我配置技术参数；能从代码、文件或服务器查到的自行查。

我的解压目录：由我补充。
我的第一个需求：由我补充。
