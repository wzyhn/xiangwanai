"use strict";

// 守护 tdesign 打包裁剪的一致性:
// - WXML 用到的 <t-*> 必须在 app.json 全局注册;
// - 依赖闭包(usingComponents 图 + JS import 图,两级都要)内的 tdesign 目录
//   不得出现在 project.config.json packOptions.ignore;
// - ignore 里的 tdesign 目录必须真实存在且不在闭包内。
// 教训:calendar/overlay/popup 的 JS 依赖 ../mixins/,只看 usingComponents 会
// 把 mixins 错裁掉,日历运行时崩。

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const APP_ROOT = path.join(__dirname, "..");
const TDESIGN_ROOT = path.join(APP_ROOT, "miniprogram_npm", "tdesign-miniprogram");

function readJSON(relativePath) {
  return JSON.parse(fs.readFileSync(path.join(APP_ROOT, relativePath), "utf8"));
}

function collectUsedTags() {
  const used = new Set();
  const pagesRoot = path.join(APP_ROOT, "pages");
  for (const entry of fs.readdirSync(pagesRoot)) {
    const wxml = path.join(pagesRoot, entry, "index.wxml");
    if (!fs.existsSync(wxml)) continue;
    for (const match of fs.readFileSync(wxml, "utf8").matchAll(/<t-[a-z-]+/g)) {
      used.add(match[0].slice(1));
    }
  }
  const tabBarWxml = path.join(APP_ROOT, "custom-tab-bar", "index.wxml");
  if (fs.existsSync(tabBarWxml)) {
    for (const match of fs.readFileSync(tabBarWxml, "utf8").matchAll(/<t-[a-z-]+/g))
      used.add(match[0].slice(1));
  }
  return used;
}

function dirOfComponentPath(componentPath) {
  const parts = String(componentPath || "").split("/");
  return parts.length >= 2 ? parts[parts.length - 2] : null;
}

// usingComponents 图:组件 json 内 "../xxx/yyy" 引用的兄弟目录。
function componentJsonDeps(dir) {
  const deps = new Set();
  const dirPath = path.join(TDESIGN_ROOT, dir);
  if (!fs.existsSync(dirPath)) return deps;
  for (const entry of fs.readdirSync(dirPath)) {
    if (!entry.endsWith(".json")) continue;
    let parsed;
    try {
      parsed = JSON.parse(fs.readFileSync(path.join(dirPath, entry), "utf8"));
    } catch (_error) {
      continue;
    }
    for (const target of Object.values(parsed.usingComponents || {})) {
      const sibling = /^\.\.\/([a-z0-9-]+)\//.exec(String(target));
      if (sibling) deps.add(sibling[1]);
    }
  }
  return deps;
}

// JS import 图:组件 js 内相对路径 import 解析到闭包外目录时必须捕获。
function componentJsDeps(dir) {
  const deps = new Set();
  const dirPath = path.join(TDESIGN_ROOT, dir);
  if (!fs.existsSync(dirPath)) return deps;
  const walk = (current) => {
    for (const entry of fs.readdirSync(current)) {
      const entryPath = path.join(current, entry);
      const stat = fs.statSync(entryPath);
      if (stat.isDirectory()) {
        walk(entryPath);
        continue;
      }
      if (!entry.endsWith(".js")) continue;
      const source = fs.readFileSync(entryPath, "utf8");
      for (const match of source.matchAll(/from\s*["']((?:\.{1,2}\/)[^"']+)["']/g)) {
        const resolved = path.resolve(path.dirname(entryPath), match[1]);
        const topSegment = path.relative(TDESIGN_ROOT, resolved).split(path.sep)[0];
        if (topSegment && !topSegment.includes(".")) deps.add(topSegment);
      }
    }
  };
  walk(dirPath);
  return deps;
}

function dependencyClosure(seedDirs) {
  const closure = new Set(seedDirs);
  closure.add("common");
  let grew = true;
  while (grew) {
    grew = false;
    for (const dir of [...closure]) {
      for (const dep of [...componentJsonDeps(dir), ...componentJsDeps(dir)]) {
        if (!closure.has(dep) && fs.existsSync(path.join(TDESIGN_ROOT, dep))) {
          closure.add(dep);
          grew = true;
        }
      }
    }
  }
  return closure;
}

test("tdesign pack trim stays consistent with actual component usage", () => {
  const app = readJSON("app.json");
  const project = readJSON("project.config.json");
  const usedTags = collectUsedTags();

  // 1) WXML 用到的组件必须已注册(未注册时真机直接渲染失败)。
  for (const tag of usedTags) {
    assert.ok(
      app.usingComponents && app.usingComponents[tag],
      `${tag} 在页面 WXML 中使用但未在 app.json 全局注册`,
    );
  }

  // 2) 从"实际使用的组件"出发计算两级依赖闭包。
  const seedDirs = [...usedTags]
    .map((tag) => dirOfComponentPath(app.usingComponents[tag]))
    .filter(Boolean);
  const closure = dependencyClosure(seedDirs);

  // 3) 全局注册指向的目录必须在闭包内(注册即可能被注入,不能被裁掉)。
  for (const [tag, componentPath] of Object.entries(app.usingComponents || {})) {
    const dir = dirOfComponentPath(componentPath);
    if (dir && String(componentPath).includes("tdesign-miniprogram")) {
      assert.ok(closure.has(dir), `app.json 注册的 ${tag} 依赖目录 ${dir} 不在使用闭包内`);
    }
  }

  // 4) ignore 的 tdesign 目录必须存在,且不在闭包内;闭包内的目录绝不能被 ignore。
  //    miniprogram_npm 是本地构建产物、不进 git:干净 checkout(如 CI)上目录
  //    尚不存在,目录级断言只在产物存在时强制(2026-09-19 codex follow-up)。
  const tdesignBuilt = fs.existsSync(TDESIGN_ROOT);
  const ignoredFolders = ((project.packOptions && project.packOptions.ignore) || [])
    .filter(
      (entry) =>
        entry.type === "folder" &&
        String(entry.value || "").startsWith("miniprogram_npm/tdesign-miniprogram/"),
    )
    .map((entry) => path.basename(entry.value));
  for (const folder of ignoredFolders) {
    if (tdesignBuilt) {
      assert.ok(
        fs.existsSync(path.join(TDESIGN_ROOT, folder)),
        `project.config.json ignore 了不存在的 tdesign 目录 ${folder}`,
      );
    }
    assert.ok(!closure.has(folder), `依赖闭包内的目录 ${folder} 被 packOptions.ignore 裁掉`);
  }
  const ignoredSet = new Set(ignoredFolders);
  if (tdesignBuilt) {
    for (const dir of closure) {
      assert.ok(!ignoredSet.has(dir), `依赖闭包内的目录 ${dir} 被 packOptions.ignore 裁掉`);
    }
  }

  // 5) tdesign 1.15.3 的 package.json 不声明 dependencies,但其编译产物
  // import "tslib"——必须由本应用直接依赖 tslib,否则「构建 npm」不会产出
  // miniprogram_npm/tslib,14 个组件集体解析失败(2026-09-19 真机日志教训)。
  const declared = readJSON("package.json").dependencies || {};
  assert.ok(
    declared["tdesign-miniprogram"] && declared.tslib && declared.dayjs,
    "tdesign 在依赖中时,tslib 与 dayjs 必须同时是直接依赖(tdesign 不声明它们,构建 npm 才能产出顶层包)",
  );
  const tslibEntry = path.join(APP_ROOT, "miniprogram_npm", "tslib", "package.json");
  if (fs.existsSync(TDESIGN_ROOT)) {
    assert.ok(
      fs.existsSync(tslibEntry),
      "miniprogram_npm/tslib 缺失:重新执行微信开发者工具「构建 npm」",
    );
  }

  // 5b) tdesign 的中文语言包 import "dayjs/locale/zh-cn";DevTools 构建器会把
  // dayjs 拍扁成单文件 bundle 丢掉 locale/,必须跑 sync-miniprogram-registry-
  // packages.mjs 全量恢复(2026-09-19 calendar 组件故障根因)。
  if (fs.existsSync(TDESIGN_ROOT)) {
    assert.ok(
      fs.existsSync(path.join(APP_ROOT, "miniprogram_npm", "dayjs", "locale", "zh-cn.js")),
      "miniprogram_npm/dayjs/locale/zh-cn.js 缺失:构建 npm 后执行 node scripts/sync-miniprogram-registry-packages.mjs",
    );
  }

  // 6) 依赖过滤开关必须关闭:DevTools 的静态分析看不懂 tdesign 产物里的跨包裸
  // require('tslib'),开着 ignoreDev/UploadUnusedFiles 会把 tslib 判成"无依赖"
  // 剔出编译/上传包 —— 文件在磁盘、模块表缺失,"module is not defined" 的精确
  // 签名(2026-09-19 终局定位,cli auto 编译不走该过滤所以自动化跑通而手动编译炸)。
  for (const configFile of ["project.config.json", "project.private.config.json"]) {
    const configPath = path.join(APP_ROOT, configFile);
    if (!fs.existsSync(configPath)) continue;
    const settings = readJSON(configFile).setting || {};
    assert.notEqual(
      settings.ignoreDevUnusedFiles,
      true,
      `${configFile} 开启了 ignoreDevUnusedFiles:会把 miniprogram_npm/tslib 剔出编译`,
    );
    assert.notEqual(
      settings.ignoreUploadUnusedFiles,
      true,
      `${configFile} 开启了 ignoreUploadUnusedFiles:会把 miniprogram_npm/tslib 剔出上传包`,
    );
  }
});
