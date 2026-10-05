"use strict";

const controller = require("../../features/checkin-credential/controller");

if (typeof Page === "function") Page(controller.createCheckinCredentialPageDefinition());

module.exports = controller;
