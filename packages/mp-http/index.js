const { createClient, createRequestError } = require("./lib/client");
const { createDefaultClient } = require("./lib/default-client");
const { generateIdempotencyKey, generateRequestId } = require("./lib/request-id");
const errorMapper = require("./lib/error-mapper");

module.exports = {
  createClient,
  createDefaultClient,
  createRequestError,
  generateRequestId,
  generateIdempotencyKey,
  mapErrorToToast: errorMapper.mapErrorToToast,
  getUserFacingMessage: errorMapper.getUserFacingMessage,
  errorMapper,
};
