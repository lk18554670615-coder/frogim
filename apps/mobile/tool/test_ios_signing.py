import copy
from datetime import datetime, timezone
import unittest

from validate_ios_signing import validate


class IOSSigningTest(unittest.TestCase):
    def setUp(self):
        self.now = datetime(2026, 9, 8, tzinfo=timezone.utc)
        self.bundle = "top.hongjinghuanqiu.app"
        self.profile = {
            "Name": self.bundle, "UUID": "test-profile", "TeamIdentifier": ["TESTTEAM12"],
            "ApplicationIdentifierPrefix": ["TESTTEAM12"],
            "ExpirationDate": datetime(2027, 9, 5),
            "ProvisionedDevices": ["synthetic-device"],
            "Entitlements": {"application-identifier": f"TESTTEAM12.{self.bundle}",
                             "get-task-allow": False, "aps-environment": "production"},
        }
        self.export = {"method": "release-testing", "teamID": "TESTTEAM12",
                       "signingStyle": "manual", "provisioningProfiles": {self.bundle: "test-profile"}}

    def test_ad_hoc_matches_without_logging_device_ids(self):
        message = validate(self.profile, self.export, self.bundle, self.now)
        self.assertIn("registered devices=1", message)
        self.assertNotIn("synthetic-device", message)

    def test_rejects_old_bundle_profile_and_export_mapping(self):
        for target in ["profile", "export"]:
            with self.subTest(target=target):
                profile, export = copy.deepcopy(self.profile), copy.deepcopy(self.export)
                if target == "profile":
                    profile["Entitlements"]["application-identifier"] = "TESTTEAM12.old.app"
                else:
                    export["provisioningProfiles"] = {"old.app": "test-profile"}
                with self.assertRaises(ValueError):
                    validate(profile, export, self.bundle, self.now)

    def test_expiry_boundary_is_rejected(self):
        self.profile["ExpirationDate"] = self.now
        with self.assertRaisesRegex(ValueError, "expired"):
            validate(self.profile, self.export, self.bundle, self.now)

    def test_ad_hoc_cannot_be_exported_as_enterprise(self):
        self.export["method"] = "enterprise"
        with self.assertRaisesRegex(ValueError, "distribution"):
            validate(self.profile, self.export, self.bundle, self.now)

    def test_wrong_team_and_profile_are_rejected(self):
        for key, value in [("teamID", "WRONGTEAM1"), ("signingStyle", "automatic"),
                           ("provisioningProfiles", {self.bundle: "wrong-profile"})]:
            with self.subTest(key=key):
                export = {**self.export, key: value}
                with self.assertRaises(ValueError):
                    validate(self.profile, export, self.bundle, self.now)

    def test_missing_push_entitlement_is_rejected(self):
        del self.profile["Entitlements"]["aps-environment"]
        with self.assertRaisesRegex(ValueError, "APNs"):
            validate(self.profile, self.export, self.bundle, self.now)

    def test_store_and_enterprise_profiles_require_matching_export(self):
        for enterprise in [False, True]:
            with self.subTest(enterprise=enterprise):
                profile = copy.deepcopy(self.profile)
                del profile["ProvisionedDevices"]
                profile["ProvisionsAllDevices"] = enterprise
                export = {**self.export, "method": "enterprise" if enterprise else "app-store-connect"}
                validate(profile, export, self.bundle, self.now)


if __name__ == "__main__":
    unittest.main()
