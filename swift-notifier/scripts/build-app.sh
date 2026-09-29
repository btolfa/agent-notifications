#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(dirname "$PROJECT_DIR")"

BINARY_NAME="terminal-notifier-modern"
APP_BUNDLE_NAME="ClaudeNotifier"
BUILD_DIR="${PROJECT_DIR}/.build"
APP_BUNDLE="${PROJECT_DIR}/${APP_BUNDLE_NAME}.app"
ICON_SRC="${REPO_ROOT}/claude_icon.png"
ENTITLEMENTS="${PROJECT_DIR}/entitlements.plist"
CI_ENTITLEMENTS="${PROJECT_DIR}/entitlements-ci.plist"

CI_MODE=false
SKIP_NOTARIZE=false
REGISTER_APP=true
PROFILE_ARG=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        --ci) CI_MODE=true ;;
        --skip-notarize) SKIP_NOTARIZE=true ;;
        --no-register) REGISTER_APP=false ;;
        --provisioning-profile)
            if [ "$#" -lt 2 ]; then
                echo "Error: --provisioning-profile requires a path"
                exit 1
            fi
            PROFILE_ARG="$2"
            shift
            ;;
        *)
            echo "Unknown argument: $1"
            exit 1
            ;;
    esac
    shift
done

echo "Building ${BINARY_NAME}..."
if [ "$CI_MODE" = true ]; then
    echo "  Mode: CI (Developer ID + hardened runtime + notarization)"
else
    echo "  Mode: Local (ad-hoc signing)"
fi

cd "$PROJECT_DIR"

# Optional provisioning profile. `com.apple.developer.usernotifications.time-sensitive`
# is a restricted entitlement: AMFI refuses to launch the process unless a matching
# Developer ID provisioning profile is embedded at Contents/embedded.provisionprofile
# (failure looks like AppleMobileFileIntegrityError -413 "No matching profile found").
# Restricted entitlements only work for Developer ID signatures, so the profile is
# applied in --ci mode only.
#
# Sources, in order of precedence: --provisioning-profile <path>, PROVISIONING_PROFILE,
# PROVISIONING_PROFILE_BASE64 (for CI secrets).
PROFILE_PATH=""
PROFILE_APP_ID=""
PROFILE_TEAM_ID=""
PROFILE_PLIST_FILE=""
PROFILE_TMP_DIR=""
PROFILE_APPLIED=false

cleanup_profile_tmp() {
    if [ -n "$PROFILE_TMP_DIR" ]; then
        rm -rf "$PROFILE_TMP_DIR"
    fi
}
trap cleanup_profile_tmp EXIT

read_profile() {
    /usr/libexec/PlistBuddy -c "Print $1" "$PROFILE_PLIST_FILE" 2>/dev/null | grep -v "Does Not Exist" || true
}

# PlistBuddy treats ':' as the path separator, so entitlement keys containing dots
# (com.apple.*) survive intact. plutil would misread those dots as a key path.
add_or_replace_entitlement() {
    local key="$1" value="$2" plist="$3"
    /usr/libexec/PlistBuddy -c "Delete :${key}" "$plist" >/dev/null 2>&1 || true
    /usr/libexec/PlistBuddy -c "Add :${key} string ${value}" "$plist" >/dev/null
}

if [ -n "$PROFILE_ARG" ]; then
    PROFILE_PATH="$PROFILE_ARG"
elif [ -n "${PROVISIONING_PROFILE:-}" ]; then
    PROFILE_PATH="$PROVISIONING_PROFILE"
elif [ -n "${PROVISIONING_PROFILE_BASE64:-}" ]; then
    PROFILE_TMP_DIR="$(mktemp -d)"
    PROFILE_PATH="${PROFILE_TMP_DIR}/embedded.provisionprofile"
    if ! printf '%s' "$PROVISIONING_PROFILE_BASE64" | base64 --decode > "$PROFILE_PATH" 2>/dev/null; then
        if ! printf '%s' "$PROVISIONING_PROFILE_BASE64" | base64 -D > "$PROFILE_PATH" 2>/dev/null; then
            echo "Error: could not base64-decode PROVISIONING_PROFILE_BASE64"
            exit 1
        fi
    fi
fi

# Local convenience: a Developer ID build picks up a profile dropped next to the
# sources without extra flags. Restricted to --ci mode so a plain
# 'make build-notifier' keeps behaving exactly as before.
if [ -z "$PROFILE_PATH" ] && [ "$CI_MODE" = true ]; then
    for candidate in "${PROJECT_DIR}/embedded.provisionprofile" "${REPO_ROOT}/embedded.provisionprofile"; do
        if [ -f "$candidate" ]; then
            PROFILE_PATH="$candidate"
            echo "Auto-detected provisioning profile: ${candidate}"
            break
        fi
    done
fi

if [ -n "$PROFILE_PATH" ]; then
    if [ ! -f "$PROFILE_PATH" ]; then
        echo "Error: provisioning profile not found at ${PROFILE_PATH}"
        exit 1
    fi

    if [ -z "$PROFILE_TMP_DIR" ]; then
        PROFILE_TMP_DIR="$(mktemp -d)"
    fi
    PROFILE_PLIST_FILE="${PROFILE_TMP_DIR}/profile.plist"
    if ! security cms -D -i "$PROFILE_PATH" > "$PROFILE_PLIST_FILE" 2>/dev/null; then
        echo "Error: ${PROFILE_PATH} is not a readable provisioning profile"
        exit 1
    fi

    PROFILE_NAME="$(read_profile :Name)"
    PROFILE_UUID="$(read_profile :UUID)"
    PROFILE_TEAM_ID="$(read_profile :TeamIdentifier:0)"
    # plutil renders dates as ISO8601, which compares lexicographically against
    # `date -u`. PlistBuddy would print "Wed Sep 01 02:00:00 CET 2027" instead.
    PROFILE_EXPIRATION="$(plutil -extract ExpirationDate raw -o - "$PROFILE_PLIST_FILE" 2>/dev/null || true)"
    PROFILE_APP_ID="$(read_profile :Entitlements:application-identifier)"
    if [ -z "$PROFILE_APP_ID" ]; then
        PROFILE_APP_ID="$(read_profile :Entitlements:com.apple.application-identifier)"
    fi

    BUNDLE_ID="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${PROJECT_DIR}/Resources/Info.plist")"

    if [ -z "$PROFILE_TEAM_ID" ] || [ -z "$PROFILE_APP_ID" ]; then
        echo "Error: ${PROFILE_PATH} has no TeamIdentifier/application-identifier."
        echo "  Expected a macOS Developer ID provisioning profile."
        exit 1
    fi

    if [ "$PROFILE_APP_ID" != "${PROFILE_TEAM_ID}.${BUNDLE_ID}" ]; then
        echo "Error: profile is for '${PROFILE_APP_ID}' but this bundle is '${PROFILE_TEAM_ID}.${BUNDLE_ID}'."
        echo "  Register the App ID for ${BUNDLE_ID} and regenerate the profile."
        exit 1
    fi

    if [ -n "$PROFILE_EXPIRATION" ] && [ "$PROFILE_EXPIRATION" \< "$(date -u +%Y-%m-%dT%H:%M:%SZ)" ]; then
        echo "Error: provisioning profile expired at ${PROFILE_EXPIRATION}"
        exit 1
    fi

    echo "Provisioning profile: ${PROFILE_NAME:-<unnamed>} (${PROFILE_UUID:-no UUID})"
    echo "  App ID:  ${PROFILE_APP_ID}"
    echo "  Expires: ${PROFILE_EXPIRATION:-unknown}"
fi

# Build both architectures in one invocation: SwiftPM emits a universal product
# on Darwin when --arch is repeated. Two separate per-arch builds no longer work
# because Swift 6 moved products to .build/out/Products/Release and reuses a
# single scratch path across architectures, so the second build overwrites the
# first instead of failing loudly.
echo "Building universal binary (arm64 + x86_64)..."
swift build -c release --arch arm64 --arch x86_64 2>&1

# Ask SwiftPM where the product landed rather than hardcoding the layout, so this
# keeps working across toolchain versions.
BIN_PATH="$(swift build -c release --arch arm64 --arch x86_64 --show-bin-path)"
BINARY="${BIN_PATH}/${BINARY_NAME}"
if [ ! -f "$BINARY" ]; then
    echo "Error: universal binary not found at ${BINARY}"
    exit 1
fi

# Fail closed on a thin binary: arm64-only would be silently unusable on Intel
# Macs and vice versa.
ARCHS="$(lipo -archs "$BINARY" 2>/dev/null || true)"
has_arch() { case " ${ARCHS} " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }
if ! has_arch arm64 || ! has_arch x86_64; then
    echo "Error: expected a universal arm64 + x86_64 binary, got: ${ARCHS:-unknown}"
    exit 1
fi

echo "Universal binary built successfully: ${BINARY}"
file "$BINARY"

echo "Assembling .app bundle..."

rm -rf "$APP_BUNDLE"
mkdir -p "${APP_BUNDLE}/Contents/MacOS"
mkdir -p "${APP_BUNDLE}/Contents/Resources"

cp "$BINARY" "${APP_BUNDLE}/Contents/MacOS/${BINARY_NAME}"
cp "${PROJECT_DIR}/Resources/Info.plist" "${APP_BUNDLE}/Contents/"

if [ -n "$PROFILE_PATH" ]; then
    if [ "$CI_MODE" = true ]; then
        cp "$PROFILE_PATH" "${APP_BUNDLE}/Contents/embedded.provisionprofile"
        echo "Embedded provisioning profile at Contents/embedded.provisionprofile"

        # AMFI kills the process at launch if the signature claims a restricted
        # entitlement the profile does not authorize. Catch that here, where the
        # message can name the capability, instead of failing the smoke check with
        # an opaque "Launch failed".
        if [ -f "$ENTITLEMENTS" ]; then
            MISSING_ENTITLEMENTS=""
            while IFS= read -r restricted_key; do
                if [ -n "$restricted_key" ]; then
                    if [ -z "$(read_profile ":Entitlements:${restricted_key}")" ]; then
                        MISSING_ENTITLEMENTS="${MISSING_ENTITLEMENTS} ${restricted_key}"
                    fi
                fi
            done < <(grep -o '<key>com\.apple\.developer\.[^<]*</key>' "$ENTITLEMENTS" | sed -e 's|<key>||' -e 's|</key>||')

            if [ -n "$MISSING_ENTITLEMENTS" ]; then
                echo "Error: profile does not authorize:${MISSING_ENTITLEMENTS}"
                echo "  Enable the matching capability on the App ID (Time Sensitive Notifications"
                echo "  covers com.apple.developer.usernotifications.time-sensitive), regenerate"
                echo "  the profile, then rebuild."
                exit 1
            fi
            echo "Restricted entitlements authorized by profile"
        fi
    else
        echo "Warning: provisioning profile ignored in local mode."
        echo "  Ad-hoc signatures cannot carry restricted entitlements. Use"
        echo "  '--ci --skip-notarize' to sign with Developer ID and apply the profile."
    fi
fi

if [ -f "$ICON_SRC" ]; then
    echo "Generating app icon..."
    ICONSET_DIR="$(mktemp -d)/AppIcon.iconset"
    mkdir -p "$ICONSET_DIR"

    sips -z 16 16 "$ICON_SRC" --out "$ICONSET_DIR/icon_16x16.png" 2>/dev/null || true
    sips -z 32 32 "$ICON_SRC" --out "$ICONSET_DIR/icon_16x16@2x.png" 2>/dev/null || true
    sips -z 32 32 "$ICON_SRC" --out "$ICONSET_DIR/icon_32x32.png" 2>/dev/null || true
    sips -z 64 64 "$ICON_SRC" --out "$ICONSET_DIR/icon_32x32@2x.png" 2>/dev/null || true
    sips -z 128 128 "$ICON_SRC" --out "$ICONSET_DIR/icon_128x128.png" 2>/dev/null || true
    sips -z 256 256 "$ICON_SRC" --out "$ICONSET_DIR/icon_128x128@2x.png" 2>/dev/null || true
    sips -z 256 256 "$ICON_SRC" --out "$ICONSET_DIR/icon_256x256.png" 2>/dev/null || true
    sips -z 512 512 "$ICON_SRC" --out "$ICONSET_DIR/icon_256x256@2x.png" 2>/dev/null || true
    sips -z 512 512 "$ICON_SRC" --out "$ICONSET_DIR/icon_512x512.png" 2>/dev/null || true

    ICNS_PATH="${APP_BUNDLE}/Contents/Resources/AppIcon.icns"
    if iconutil -c icns "$ICONSET_DIR" -o "$ICNS_PATH" 2>/dev/null; then
        echo "App icon generated successfully"
    else
        echo "Warning: could not generate app icon (iconutil failed)"
    fi

    rm -rf "$(dirname "$ICONSET_DIR")"
else
    echo "Warning: icon source not found at ${ICON_SRC}, skipping icon generation"
fi

# Covered by the bundle resource seal. Old bundles lack this marker and must
# never be invoked with a capability flag by the managed installer.
cat > "${APP_BUNDLE}/Contents/Resources/managed-runtime.json" <<'MANIFEST'
{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}
MANIFEST

sign_with_entitlements() {
    local entitlements_path="$1"
    local label="$2"

    local flags=(--force --timestamp --options runtime)
    if [ -n "$entitlements_path" ]; then
        flags+=(--entitlements "$entitlements_path")
        echo "Using entitlements: ${entitlements_path}"
    else
        echo "Using entitlements: none"
    fi

    echo "Code signing with: Developer ID Application (hardened runtime) [${label}]"
    codesign "${flags[@]}" --sign "Developer ID Application" "${APP_BUNDLE}"
    codesign --verify --verbose "${APP_BUNDLE}"
    echo "Signature verified"

    if ! open -W -n "${APP_BUNDLE}" --args -launchedViaLaunchServices -help >/dev/null 2>&1; then
        echo "Error: signed app failed LaunchServices smoke check (-help) [${label}]"
        # A failed spawn leaves launchd briefly holding the bundle, and a codesign
        # immediately afterwards fails with "Operation not permitted" instead of
        # replacing the signature. Clear any child and let LaunchServices settle so
        # the CI-safe entitlements fallback in the caller can actually re-sign.
        pkill -f "${BINARY_NAME}" 2>/dev/null || true
        sleep 1
        return 1
    fi

    return 0
}

if [ "$CI_MODE" = true ]; then
    FULL_ENTITLEMENTS=""
    SAFE_ENTITLEMENTS=""
    if [ -f "$ENTITLEMENTS" ]; then
        FULL_ENTITLEMENTS="$ENTITLEMENTS"
    fi
    if [ -f "$CI_ENTITLEMENTS" ]; then
        SAFE_ENTITLEMENTS="$CI_ENTITLEMENTS"
    fi

    # The signature must also carry the profile's identifier keys, otherwise AMFI
    # cannot match the embedded profile to the code. Derive a copy instead of
    # editing entitlements.plist, so the no-profile path stays byte-for-byte the
    # same and keeps working without a profile.
    if [ -n "$PROFILE_PATH" ] && [ -n "$FULL_ENTITLEMENTS" ]; then
        mkdir -p "$BUILD_DIR"
        DERIVED_ENTITLEMENTS="${BUILD_DIR}/entitlements-with-profile.plist"
        cp "$FULL_ENTITLEMENTS" "$DERIVED_ENTITLEMENTS"
        add_or_replace_entitlement "com.apple.application-identifier" "$PROFILE_APP_ID" "$DERIVED_ENTITLEMENTS"
        add_or_replace_entitlement "com.apple.developer.team-identifier" "$PROFILE_TEAM_ID" "$DERIVED_ENTITLEMENTS"
        FULL_ENTITLEMENTS="$DERIVED_ENTITLEMENTS"
        PROFILE_APPLIED=true
        echo "Derived entitlements carrying profile identity: ${DERIVED_ENTITLEMENTS}"
    fi

    EFFECTIVE_ENTITLEMENTS="$FULL_ENTITLEMENTS"
    EFFECTIVE_LABEL="full"

    if ! sign_with_entitlements "$EFFECTIVE_ENTITLEMENTS" "$EFFECTIVE_LABEL"; then
        if [ -n "$SAFE_ENTITLEMENTS" ] && [ "$SAFE_ENTITLEMENTS" != "$FULL_ENTITLEMENTS" ]; then
            echo "Warning: full entitlements failed runtime smoke check, retrying with CI-safe entitlements"
            EFFECTIVE_ENTITLEMENTS="$SAFE_ENTITLEMENTS"
            EFFECTIVE_LABEL="ci-safe"
            sign_with_entitlements "$EFFECTIVE_ENTITLEMENTS" "$EFFECTIVE_LABEL"
        else
            exit 1
        fi
    fi
else
    # No --deep: the bundle has no nested code, and signing with --deep is
    # deprecated by Apple because it can seal the wrong thing. The CI path never
    # used it either.
    echo "Code signing .app bundle (ad-hoc)..."
    codesign --force --sign - "$APP_BUNDLE" 2>/dev/null || {
        echo "Warning: code signing failed (notifications may require manual permission)"
    }
fi

if [ "$CI_MODE" = true ] && [ "$SKIP_NOTARIZE" != true ]; then
    echo ""
    echo "Notarizing ${APP_BUNDLE_NAME}.app..."

    if [ -z "${APPLE_ID:-}" ] || [ -z "${APPLE_PASSWORD:-}" ] || [ -z "${APPLE_TEAM_ID:-}" ]; then
        echo "Error: APPLE_ID, APPLE_PASSWORD, and APPLE_TEAM_ID must be set for notarization"
        exit 1
    fi

    NOTARIZE_ZIP="${BUILD_DIR}/${APP_BUNDLE_NAME}-notarize.zip"
    ditto -c -k --keepParent "${APP_BUNDLE}" "${NOTARIZE_ZIP}"

    xcrun notarytool submit "${NOTARIZE_ZIP}" \
        --apple-id "${APPLE_ID}" \
        --password "${APPLE_PASSWORD}" \
        --team-id "${APPLE_TEAM_ID}" \
        --wait

    rm -f "${NOTARIZE_ZIP}"

    echo "Stapling notarization ticket..."
    xcrun stapler staple "${APP_BUNDLE}"

    echo "Verifying notarized bundle..."
    codesign --verify --verbose "${APP_BUNDLE}"
    spctl --assess --type execute --verbose "${APP_BUNDLE}"
    echo "Notarization complete!"
fi

# Hash final signed bytes outside the bundle's own resource seal. The release
# archive transports this attestation alongside the app; it is not a publisher
# certificate, and unmanaged existing bundles are never probed on its authority.
cat > "${APP_BUNDLE}.managed-runtime.json" <<MANIFEST
{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1,"ExecutableSHA256":"$(shasum -a 256 "${APP_BUNDLE}/Contents/MacOS/${BINARY_NAME}" | awk '{print $1}')"}
MANIFEST

LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
# Packaging and isolated qualification must not register a temporary bundle as
# the user's callback handler. Registration belongs to the stable installed path.
if [ "$REGISTER_APP" = true ] && [ -x "$LSREGISTER" ]; then
    "$LSREGISTER" -f "$APP_BUNDLE" 2>/dev/null || true
    echo "Registered with Launch Services"
fi

echo ""
echo "Build complete!"
echo "  Binary: ${APP_BUNDLE}/Contents/MacOS/${BINARY_NAME}"
echo "  Bundle: ${APP_BUNDLE}"
if [ "$CI_MODE" = true ]; then
    echo "  Signed: Developer ID Application (hardened runtime)"
    if [ "$PROFILE_APPLIED" = true ]; then
        echo "  Provisioning profile: embedded (time-sensitive notifications authorized)"
    fi
    if [ "$SKIP_NOTARIZE" != true ]; then
        echo "  Notarized: yes"
    fi
fi
echo ""
echo "To install into plugin bin/:"
echo "  cp -R ${APP_BUNDLE} ${APP_BUNDLE}.managed-runtime.json ${REPO_ROOT}/bin/"
