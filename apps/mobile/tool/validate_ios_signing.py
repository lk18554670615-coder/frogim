"""Validate decoded CI signing metadata without logging certificates or UDIDs."""
import argparse
from datetime import datetime, timezone
from pathlib import Path
import plistlib


def validate(profile, export, bundle_id, now=None):
    now = now or datetime.now(timezone.utc)
    entitlements = profile.get("Entitlements", {})
    teams = profile.get("TeamIdentifier", [])
    prefixes = profile.get("ApplicationIdentifierPrefix", [])
    if entitlements.get("application-identifier") not in {
        f"{prefix}.{bundle_id}" for prefix in prefixes
    }:
        raise ValueError("Provisioning profile does not match the app Bundle ID")
    expiry = profile.get("ExpirationDate")
    if not isinstance(expiry, datetime) or expiry.replace(tzinfo=timezone.utc) <= now:
        raise ValueError("Provisioning profile has expired or has no expiry")
    if export.get("teamID") not in teams:
        raise ValueError("Export team does not match the provisioning profile")
    if export.get("signingStyle") != "manual":
        raise ValueError("This workflow requires manual export signing")
    selected = export.get("provisioningProfiles", {}).get(bundle_id)
    if not selected or selected not in {profile.get("Name"), profile.get("UUID")}:
        raise ValueError("Export options do not select this provisioning profile")
    devices = profile.get("ProvisionedDevices", [])
    development = entitlements.get("get-task-allow", False)
    if profile.get("ProvisionsAllDevices") and not development:
        allowed = {"enterprise"}
    elif devices:
        allowed = {"debugging", "development"} if development else {"release-testing", "ad-hoc"}
    elif not development:
        allowed = {"app-store-connect", "app-store"}
    else:
        raise ValueError("Unsupported provisioning profile distribution type")
    if export.get("method") not in allowed:
        raise ValueError("Export method does not match the profile distribution type")
    if entitlements.get("aps-environment") != ("development" if development else "production"):
        raise ValueError("Provisioning profile must include matching APNs entitlement")
    return f"Signing metadata matches {bundle_id}; method={export['method']}; registered devices={len(devices)}"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--export-options", type=Path, required=True)
    parser.add_argument("--bundle-id", required=True)
    args = parser.parse_args()
    try:
        print(validate(plistlib.loads(args.profile.read_bytes()),
                       plistlib.loads(args.export_options.read_bytes()), args.bundle_id))
    except (ValueError, TypeError, plistlib.InvalidFileException) as error:
        parser.exit(1, f"Signing metadata validation failed: {error}\n")


if __name__ == "__main__":
    main()
