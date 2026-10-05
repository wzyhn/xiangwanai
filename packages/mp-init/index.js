const { bootstrapApp } = require("./lib/bootstrap");
const runtimeEnv = require("./lib/runtime-env");

module.exports = {
  bootstrapApp,
  runtimeEnv,
  DEFAULT_API_BASE_URL: runtimeEnv.DEFAULT_API_BASE_URL,
  LOCAL_API_BASE_URL: runtimeEnv.LOCAL_API_BASE_URL,
  PRODUCTION_API_BASE_URL: runtimeEnv.PRODUCTION_API_BASE_URL,
  isDevelopmentToolsEnabled: runtimeEnv.isDevelopmentToolsEnabled,
  readMiniProgramEnvVersion: runtimeEnv.readMiniProgramEnvVersion,
  resolveDefaultApiBaseUrl: runtimeEnv.resolveDefaultApiBaseUrl,
};
