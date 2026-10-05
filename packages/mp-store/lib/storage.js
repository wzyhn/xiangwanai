function getString(key, fallback = "") {
  try {
    const value = wx.getStorageSync(String(key || ""));
    return typeof value === "string" ? value : fallback;
  } catch (error) {
    return fallback;
  }
}

function setString(key, value) {
  wx.setStorageSync(String(key || ""), String(value || ""));
}

function getJSON(key, fallback = null) {
  const raw = getString(key, "");
  if (!raw) {
    return fallback;
  }
  try {
    return JSON.parse(raw);
  } catch (error) {
    return fallback;
  }
}

function setJSON(key, value) {
  setString(key, JSON.stringify(value));
}

function remove(key) {
  try {
    wx.removeStorageSync(String(key || ""));
  } catch (error) {
    // no-op
  }
}

module.exports = {
  getJSON,
  getString,
  remove,
  setJSON,
  setString,
};
