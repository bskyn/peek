#!/usr/bin/env node

const https = require("https");
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");
const os = require("os");

const REPO = "bskyn/peek";
const BIN_NAME = "peek";
const REPO_ROOT = path.resolve(__dirname, "..");
const ROOT_PACKAGE_PATH = path.join(REPO_ROOT, "package.json");
const VERSION_VAR = "github.com/bskyn/peek/internal/cli.version";

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
  const pkg = require(ROOT_PACKAGE_PATH);
  return pkg.version;
}

function getRequestedRef() {
  const specs = [process.env.npm_package_resolved, process.env.npm_package_from].filter(Boolean);

  for (const spec of specs) {
    const hashIndex = spec.lastIndexOf("#");
    if (hashIndex === -1 || hashIndex === spec.length - 1) {
      continue;
    }

    return spec.slice(hashIndex + 1);
  }

  return null;
}

function isReleaseRef(ref) {
  return /^v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/.test(ref);
}

function getReleaseTag() {
  const ref = getRequestedRef();
  if (ref && isReleaseRef(ref)) {
    return ref.startsWith("v") ? ref : `v${ref}`;
  }

  const version = getVersion();
  if (version && version !== "0.0.0" && isReleaseRef(version)) {
    return version.startsWith("v") ? version : `v${version}`;
  }

  return null;
}

function canBuildFromSource() {
  return fs.existsSync(path.join(REPO_ROOT, "go.mod")) && fs.existsSync(path.join(REPO_ROOT, "cmd", "peek", "main.go"));
}

function getBuildVersion() {
  return getReleaseTag() || getRequestedRef() || "dev";
}

async function download(url) {
  return new Promise((resolve, reject) => {
    https.get(
      url,
      {
        headers: {
          "User-Agent": `${BIN_NAME}-installer`,
        },
      },
      (res) => {
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
      }
    ).on("error", reject);
  });
}

async function installRelease(tag) {
  const { goos, goarch } = getPlatform();
  const ext = goos === "windows" ? "zip" : "tar.gz";
  const assetName = `${BIN_NAME}_${goos}_${goarch}.${ext}`;
  const url = `https://github.com/${REPO}/releases/download/${tag}/${assetName}`;

  const binDir = path.join(__dirname, "bin");
  fs.mkdirSync(binDir, { recursive: true });

  const tmpFile = path.join(os.tmpdir(), assetName);
  const data = await download(url);
  fs.writeFileSync(tmpFile, data);

  if (ext === "tar.gz") {
    execFileSync("tar", ["-xzf", tmpFile, "-C", binDir, BIN_NAME], { stdio: "inherit" });
  } else {
    execFileSync("unzip", ["-o", tmpFile, `${BIN_NAME}.exe`, "-d", binDir], { stdio: "inherit" });
  }

  fs.unlinkSync(tmpFile);

  const binPath = path.join(binDir, goos === "windows" ? `${BIN_NAME}.exe` : BIN_NAME);
  fs.chmodSync(binPath, 0o755);

  console.log(`Installed ${BIN_NAME} to ${binPath}`);
}

function installFromSource() {
  const { goos } = getPlatform();
  const binDir = path.join(__dirname, "bin");
  const output = path.join(binDir, goos === "windows" ? `${BIN_NAME}.exe` : BIN_NAME);
  const ldflags = `-X ${VERSION_VAR}=${getBuildVersion()}`;

  fs.mkdirSync(binDir, { recursive: true });

  try {
    execFileSync("go", ["build", "-ldflags", ldflags, "-o", output, "./cmd/peek"], {
      cwd: REPO_ROOT,
      stdio: "inherit",
    });
  } catch (err) {
    if (err.code === "ENOENT") {
      throw new Error("Go 1.24+ is required for source installs");
    }

    throw err;
  }

  if (goos !== "windows") {
    fs.chmodSync(output, 0o755);
  }

  console.log(`Built ${BIN_NAME} from source at ${output}`);
}

async function main() {
  const { goos, goarch } = getPlatform();
  const releaseTag = getReleaseTag();

  if (process.argv.includes("--dry-run")) {
    console.log(`Platform: ${goos}/${goarch}`);
    console.log(`Requested ref: ${getRequestedRef() || "none"}`);
    console.log(`Release tag: ${releaseTag || "none"}`);
    console.log(`Source fallback: ${canBuildFromSource() ? "available" : "unavailable"}`);
    return;
  }

  if (releaseTag) {
    try {
      console.log(`Downloading ${BIN_NAME} ${releaseTag} for ${goos}/${goarch}...`);
      await installRelease(releaseTag);
      return;
    } catch (err) {
      if (!canBuildFromSource()) {
        throw err;
      }

      console.warn(`Release install failed (${err.message}). Falling back to local Go build...`);
    }
  } else {
    console.log(`No tagged release requested. Building ${BIN_NAME} from source...`);
  }

  if (!canBuildFromSource()) {
    throw new Error("No release tag found and source build is unavailable");
  }

  installFromSource();
}

main().catch((err) => {
  console.error(`Failed to install ${BIN_NAME}: ${err.message}`);
  process.exit(1);
});
