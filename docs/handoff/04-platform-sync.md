# 复制给有私有仓库权限的维护者 AI

请将维护者已经接受到 wzyhn/xiangwanai main 的修改集成回 wzyhn/weconq，仅同步享玩允许范围。此任务由私有平台维护者执行，外部协作者不需要这项权限。

先阅读私有 docs/architecture/ARCHITECTURE.md → current-state.md，以及 deploy/xiangwan/standalone/README.md、相关 app HANDOVER 与标准。使用干净的独立 worktree，从最新 origin/main 开始。检查别人的未提交工作，不切换或重置正在使用的主工作目录。

拉取公开仓库并记录其当前 main 完整 SHA，确认维护者已经接受和该 head 的验证结果。使用私有受信任工具：

```powershell
node deploy/xiangwan/standalone/sync.mjs <公开仓库本地路径> <公开main完整SHA> --plan
node deploy/xiangwan/standalone/proposal.mjs <公开仓库本地路径> <公开main完整SHA>
```

私有 map.json 决定回写边界，不能被公开 manifest 扩大。App、小程序后台、享玩业务域、自己的迁移可映射；公开 root/workflow/共享依赖/发行支持修改需要独立评审，不自动回写平台共享实现。对方可以在独立项目修改这些文件，不能把这个事实混同为整个平台授权。

同步器若报告冲突，保留双方修改并明确列出差异；不能用公开版本覆盖私有新工作。只创建或更新一个私有集成 PR，重复运行应复用操作。私有 state 只在集成 PR 合并后成为新基线。已有迁移冻结，新迁移按私有登记规则注册。

执行私有 required checks 与 provenance/merge gate，核对精确 head 的结果。当前定时同步没有启用，历史曾有 Actions 额度阻断和 bot 不能建 PR 的设置；重新查询当前状态，不能将未执行的 CI 当通过，也不能靠个人 token 自动绕过。受信任提案阶段不运行公开脚本；业务验证在评审后的适当环境进行。

完成后报告公开 SHA、私有 PR/merge SHA、测试结果与未验证项。代码集成不等于部署；如需更新服务器，另按发布流程拿到镜像和运行回执。不要向外部协作者提供私有源码或私有仓库凭据。
