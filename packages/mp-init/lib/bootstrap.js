const { bootstrapSession } = require("mp-auth-session");

function bootstrapApp(options = {}) {
  return bootstrapSession(options);
}

module.exports = {
  bootstrapApp,
};
