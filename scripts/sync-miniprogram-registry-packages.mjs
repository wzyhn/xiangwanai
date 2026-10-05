// 构建npm后的第二步:全量恢复被 DevTools 打包器拍扁的注册表依赖。
//
// DevTools「构建 npm」对多文件 npm 包(如 dayjs)只产出按 main 入口打包的
// 单文件 bundle,丢弃子路径文件(dayjs/locale/*)——而 tdesign 的语言包以
// `import "dayjs/locale/zh-cn"` 引用它们,静态分析不可达,永不进 bundle。
// tslib 虽当前幸存,同样按全量拷贝处理以防未来版本被拍扁。
//
// 标准构建顺序(缺一不可,详见 apps/miniprogram-xiangwan/README.md):
//   1. DevTools「构建 npm」
//   2. node scripts/sync-miniprogram-workspace-packages.mjs   (workspace 包)
//   3. node scripts/sync-miniprogram-registry-packages.mjs     (本脚本)
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const workspaceRoot = path.resolve(__dirname, "..");

// 需要全量树的注册表依赖:tdesign 1.15.3 的运行时传递依赖,其自身
// package.json 不声明(dependencies 为空),必须由本应用直接依赖并全量拷贝。
const REGISTRY_PACKAGES = ["tslib", "dayjs"];

function copyPackageTree(appDir, packageName) {
  const source = path.join(appDir, "node_modules", packageName);
  const target = path.join(appDir, "miniprogram_npm", packageName);
  if (!fs.existsSync(source)) {
    console.warn(`[sync:registry] ${packageName} 不在 ${path.relative(workspaceRoot, appDir)}/node_modules,跳过`);
    return false;
  }
  fs.rmSync(target, { recursive: true, force: true });
  fs.cpSync(source, target, {
    recursive: true,
    dereference: true,
    filter: (entry) => !entry.includes(`${packageName}${path.sep}node_modules`),
  });
  return true;
}

let touched = 0;
for (const appDir of [path.join(workspaceRoot, "apps", "miniprogram-xiangwan")]) {
  for (const packageName of REGISTRY_PACKAGES) {
    if (copyPackageTree(appDir, packageName)) {
      touched += 1;
      console.log(`[sync:registry] ${path.relative(workspaceRoot, appDir)} <- ${packageName} (full tree)`);
    }
  }
}
console.log(`[sync:registry] restored ${touched} package(s)`);
