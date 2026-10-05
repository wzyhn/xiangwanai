import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const workspaceRoot = path.resolve(__dirname, "..");
const packagesRoot = path.join(workspaceRoot, "packages");
const appsRoot = path.join(workspaceRoot, "apps");
const OWNERSHIP_VERSION = 1;
const OWNERSHIP_FILENAME = ".weconq-workspace-packages.json";
const PACKAGE_MARKER_FILENAME = ".weconq-workspace-package.json";

function readJson(filePath) {
  return JSON.parse(fs.readFileSync(filePath, "utf8"));
}

function pathExists(targetPath) {
  return fs.existsSync(targetPath);
}

function listChildDirectories(rootPath) {
  if (!pathExists(rootPath)) {
    return [];
  }

  return fs
    .readdirSync(rootPath, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => path.join(rootPath, entry.name));
}

function buildWorkspacePackageMap(rootPath = packagesRoot) {
  const packageMap = new Map();

  listChildDirectories(rootPath).forEach((packageDir) => {
    const packageJsonPath = path.join(packageDir, "package.json");
    if (!pathExists(packageJsonPath)) {
      return;
    }

    const manifest = readJson(packageJsonPath);
    const packageName = String(manifest.name || "").trim();
    if (!packageName) {
      return;
    }

    packageMap.set(packageName, {
      dir: packageDir,
      manifest,
    });
  });

  return packageMap;
}

function listMiniProgramApps(rootPath = appsRoot) {
  return listChildDirectories(rootPath)
    .map((appDir) => {
      const packageJsonPath = path.join(appDir, "package.json");
      const miniprogramNpmDir = path.join(appDir, "miniprogram_npm");
      if (!pathExists(packageJsonPath)) {
        return null;
      }

      const manifest = readJson(packageJsonPath);
      const appDirectoryName = path.basename(appDir);
      const packageName = String(manifest.name || "").trim();
      if (!appDirectoryName.startsWith("miniprogram-") && !packageName.startsWith("miniprogram-")) {
        return null;
      }

      return {
        dir: appDir,
        miniprogramNpmDir,
        manifest,
      };
    })
    .filter(Boolean);
}

function collectWorkspaceDependencies(dependencies, workspacePackageMap, visited = new Set()) {
  Object.keys(dependencies || {}).forEach((dependencyName) => {
    if (visited.has(dependencyName)) {
      return;
    }

    const packageInfo = workspacePackageMap.get(dependencyName);
    if (!packageInfo) {
      return;
    }

    visited.add(dependencyName);
    collectWorkspaceDependencies(packageInfo.manifest.dependencies, workspacePackageMap, visited);
  });

  return visited;
}

function copyDirectory(sourceDir, targetDir) {
  fs.mkdirSync(targetDir, { recursive: true });

  fs.readdirSync(sourceDir, { withFileTypes: true })
    .sort((left, right) => left.name.localeCompare(right.name))
    .forEach((entry) => {
      if (entry.name === "node_modules") {
        return;
      }

      const sourcePath = path.join(sourceDir, entry.name);
      const targetPath = path.join(targetDir, entry.name);

      if (entry.isDirectory()) {
        copyDirectory(sourcePath, targetPath);
        return;
      }

      fs.mkdirSync(path.dirname(targetPath), { recursive: true });
      fs.copyFileSync(sourcePath, targetPath);
    });
}

function resolvePackageTarget(miniprogramNpmDir, packageName) {
  const normalizedName = String(packageName || "").trim();
  if (!normalizedName) {
    throw new Error("workspace package name must not be empty");
  }

  const root = path.resolve(miniprogramNpmDir);
  const target = path.resolve(root, normalizedName);
  const relative = path.relative(root, target);
  if (
    !relative ||
    relative === ".." ||
    relative.startsWith(`..${path.sep}`) ||
    path.isAbsolute(relative)
  ) {
    throw new Error(`unsafe workspace package target: ${normalizedName}`);
  }
  return target;
}

function writeJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, "utf8");
}

function writePackageMarker(targetDir, packageName) {
  writeJson(path.join(targetDir, PACKAGE_MARKER_FILENAME), {
    version: OWNERSHIP_VERSION,
    package: packageName,
  });
}

function syncWorkspacePackageToMiniProgram(packageInfo, miniprogramNpmDir) {
  const packageName = packageInfo.manifest.name;
  const targetDir = resolvePackageTarget(miniprogramNpmDir, packageName);
  fs.rmSync(targetDir, { recursive: true, force: true });
  copyDirectory(packageInfo.dir, targetDir);
  writePackageMarker(targetDir, packageName);
}

function readOwnershipManifest(miniprogramNpmDir, logger = console) {
  const manifestPath = path.join(miniprogramNpmDir, OWNERSHIP_FILENAME);
  if (!pathExists(manifestPath)) {
    return [];
  }

  try {
    const manifest = readJson(manifestPath);
    if (manifest.version !== OWNERSHIP_VERSION || !Array.isArray(manifest.packages)) {
      throw new Error("unsupported ownership manifest");
    }
    return Array.from(
      new Set(manifest.packages.map((name) => String(name || "").trim()).filter(Boolean)),
    ).sort((left, right) => left.localeCompare(right));
  } catch (error) {
    logger.warn(
      `[sync:miniprogram] ignored invalid ${OWNERSHIP_FILENAME}: ${error.message || error}`,
    );
    return [];
  }
}

function hasWorkspaceMarker(targetDir, packageName) {
  const markerPath = path.join(targetDir, PACKAGE_MARKER_FILENAME);
  if (!pathExists(markerPath)) {
    return false;
  }

  try {
    const marker = readJson(markerPath);
    return marker.version === OWNERSHIP_VERSION && marker.package === packageName;
  } catch {
    return false;
  }
}

function listRelativeFiles(rootPath, currentPath = rootPath, files = []) {
  for (const entry of fs
    .readdirSync(currentPath, { withFileTypes: true })
    .sort((left, right) => left.name.localeCompare(right.name))) {
    const absolutePath = path.join(currentPath, entry.name);
    if (entry.isDirectory()) {
      if (!listRelativeFiles(rootPath, absolutePath, files)) {
        return null;
      }
    } else if (entry.isFile()) {
      files.push(path.relative(rootPath, absolutePath));
    } else {
      return null;
    }
  }
  return files;
}

// Before ownership markers existed, the sync script copied package trees without
// metadata. Treat such a directory as ours only when its package identity still
// matches and every non-manifest file is an unchanged subset of the workspace
// source. package.json may legitimately drift when dependency versions change.
// Extra or edited implementation files make the directory manual/unknown and
// therefore ineligible for deletion.
function isLegacyWorkspaceCopy(packageInfo, targetDir) {
  const packageJsonPath = path.join(targetDir, "package.json");
  if (!pathExists(packageJsonPath)) {
    return false;
  }

  try {
    if (readJson(packageJsonPath).name !== packageInfo.manifest.name) {
      return false;
    }

    const relativeFiles = listRelativeFiles(targetDir);
    if (!relativeFiles || relativeFiles.length === 0) {
      return false;
    }

    let comparableFiles = 0;
    const matches = relativeFiles.every((relativePath) => {
      if (relativePath === "package.json") {
        return true;
      }
      if (
        relativePath === PACKAGE_MARKER_FILENAME ||
        relativePath.split(path.sep).includes("node_modules")
      ) {
        return false;
      }
      const sourcePath = path.join(packageInfo.dir, relativePath);
      comparableFiles += 1;
      return (
        pathExists(sourcePath) &&
        fs.statSync(sourcePath).isFile() &&
        fs.readFileSync(sourcePath).equals(fs.readFileSync(path.join(targetDir, relativePath)))
      );
    });
    return matches && comparableFiles > 0;
  } catch {
    return false;
  }
}

function removeStaleWorkspacePackages({
  miniprogramNpmDir,
  desiredNames,
  workspacePackageMap,
  logger = console,
}) {
  const candidates = new Set(readOwnershipManifest(miniprogramNpmDir, logger));

  // Reconsider every workspace-named directory, including pre-marker copies
  // that are absent from an existing ownership manifest. Verified source
  // copies can be cleaned; edited/manual directories are preserved and warned.
  workspacePackageMap.forEach((_packageInfo, packageName) => {
    if (pathExists(resolvePackageTarget(miniprogramNpmDir, packageName))) {
      candidates.add(packageName);
    }
  });

  const removed = [];
  const preserved = [];
  Array.from(candidates)
    .filter((packageName) => !desiredNames.has(packageName))
    .sort((left, right) => left.localeCompare(right))
    .forEach((packageName) => {
      let targetDir;
      try {
        targetDir = resolvePackageTarget(miniprogramNpmDir, packageName);
      } catch {
        preserved.push(packageName);
        return;
      }
      if (!pathExists(targetDir)) {
        return;
      }

      const packageInfo = workspacePackageMap.get(packageName);
      const owned =
        hasWorkspaceMarker(targetDir, packageName) ||
        (packageInfo && isLegacyWorkspaceCopy(packageInfo, targetDir));
      if (!owned) {
        preserved.push(packageName);
        logger.warn(
          `[sync:miniprogram] preserved unverified directory ${path.relative(
            miniprogramNpmDir,
            targetDir,
          )}`,
        );
        return;
      }

      fs.rmSync(targetDir, { recursive: true, force: true });
      removed.push(packageName);
    });

  return { removed, preserved };
}

function writeOwnershipManifest(miniprogramNpmDir, dependencyNames) {
  writeJson(path.join(miniprogramNpmDir, OWNERSHIP_FILENAME), {
    version: OWNERSHIP_VERSION,
    packages: Array.from(dependencyNames).sort((left, right) => left.localeCompare(right)),
  });
}

function run({ rootDir = workspaceRoot, logger = console } = {}) {
  const workspacePackageMap = buildWorkspacePackageMap(path.join(rootDir, "packages"));
  const miniProgramApps = listMiniProgramApps(path.join(rootDir, "apps"));

  miniProgramApps.forEach((app) => {
    fs.mkdirSync(app.miniprogramNpmDir, { recursive: true });
    const dependencyNames = collectWorkspaceDependencies(
      app.manifest.dependencies,
      workspacePackageMap,
    );

    Array.from(dependencyNames)
      .sort((left, right) => left.localeCompare(right))
      .forEach((dependencyName) => {
        const packageInfo = workspacePackageMap.get(dependencyName);
        if (!packageInfo) {
          return;
        }
        syncWorkspacePackageToMiniProgram(packageInfo, app.miniprogramNpmDir);
      });

    const cleanup = removeStaleWorkspacePackages({
      miniprogramNpmDir: app.miniprogramNpmDir,
      desiredNames: dependencyNames,
      workspacePackageMap,
      logger,
    });
    writeOwnershipManifest(app.miniprogramNpmDir, dependencyNames);

    const relativeAppPath = path.relative(rootDir, app.dir) || ".";
    const syncedCount = dependencyNames.size;
    logger.log(
      `[sync:miniprogram] ${relativeAppPath} <- ${syncedCount} workspace packages; removed ${cleanup.removed.length} stale`,
    );
  });
}

function comparablePath(filePath) {
  const resolved = path.resolve(filePath);
  return process.platform === "win32" ? resolved.toLowerCase() : resolved;
}

const entryPath = process.argv[1] ? comparablePath(process.argv[1]) : "";
if (entryPath === comparablePath(__filename)) {
  run();
}

export {
  OWNERSHIP_FILENAME,
  PACKAGE_MARKER_FILENAME,
  buildWorkspacePackageMap,
  collectWorkspaceDependencies,
  isLegacyWorkspaceCopy,
  listMiniProgramApps,
  removeStaleWorkspacePackages,
  resolvePackageTarget,
  run,
};
