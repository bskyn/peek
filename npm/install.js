#!/usr/bin/env node

const https = require("https");
const fs = require("fs");
const path = require("path");
const { execSync } = require("child_process");
const os = require("os");

const REPO = "bskyn/peek";
const BIN_NAME = "peek";

function getPlatform() {
  const platform = os.platform();
  const arch = os.arch();

  const platformMap = {
    darwin: "darwin",
    linux: "linux",
    win32: "windows",
  };

  const archMap = {
    x64: "amd64",
    arm64: "arm64",
  };

  const goos = platformMap[platform];
  const goarch = archMap[arch];

  if (!goos || !goarch) {
    throw new Error(`Unsupported platform: ${platform}/${arch}`);
  }

  return { goos, goarch };
}

function getVersion() {
  const pkg = require("./package.json");
  return pkg.version;
}

async function download(url) {
  return new Promise((resolve, reject) => {
    https.get(url, (res) => {
      if (res.statusCode === 302 || res.statusCode === 301) {
        return download(res.headers.location).then(resolve).catch(reject);
      }
      if (res.statusCode !== 200) {
        return reject(new Error(`HTTP ${res.statusCode}: ${url}`));
      }
      const chunks = [];
      res.on("data", (chunk) => chunks.push(chunk));
      res.on("end", () => resolve(Buffer.concat(chunks)));
      res.on("error", reject);
    }).on("error", reject);
  });
}

async function main() {
  if (process.argv.includes("--dry-run")) {
    const { goos, goarch } = getPlatform();
    console.log(`Platform: ${goos}/${goarch}`);
    console.log(`Version: ${getVersion()}`);
    console.log(`Would download: ${BIN_NAME}_${goos}_${goarch}.tar.gz`);
    return;
  }

  const { goos, goarch } = getPlatform();
  const version = getVersion();
  const ext = goos === "windows" ? "zip" : "tar.gz";
  const assetName = `${BIN_NAME}_${goos}_${goarch}.${ext}`;
  const url = `https://github.com/${REPO}/releases/download/v${version}/${assetName}`;

  console.log(`Downloading ${BIN_NAME} v${version} for ${goos}/${goarch}...`);

  const binDir = path.join(__dirname, "bin");
  fs.mkdirSync(binDir, { recursive: true });

  const tmpFile = path.join(os.tmpdir(), assetName);
  const data = await download(url);
  fs.writeFileSync(tmpFile, data);

  if (ext === "tar.gz") {
    execSync(`tar -xzf "${tmpFile}" -C "${binDir}" ${BIN_NAME}`, { stdio: "inherit" });
  } else {
    execSync(`unzip -o "${tmpFile}" ${BIN_NAME}.exe -d "${binDir}"`, { stdio: "inherit" });
  }

  fs.unlinkSync(tmpFile);

  const binPath = path.join(binDir, goos === "windows" ? `${BIN_NAME}.exe` : BIN_NAME);
  fs.chmodSync(binPath, 0o755);

  console.log(`Installed ${BIN_NAME} to ${binPath}`);
}

main().catch((err) => {
  console.error(`Failed to install ${BIN_NAME}: ${err.message}`);
  process.exit(1);
});
