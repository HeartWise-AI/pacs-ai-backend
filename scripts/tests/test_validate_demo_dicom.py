import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from pydicom.dataset import FileDataset, FileMetaDataset
from pydicom.uid import ExplicitVRLittleEndian, generate_uid

SCRIPT_PATH = Path(__file__).parents[1] / "validate_demo_dicom.py"
SPEC = importlib.util.spec_from_file_location("validate_demo_dicom", SCRIPT_PATH)
validator = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = validator
SPEC.loader.exec_module(validator)


def approved_policy():
    policy_path = (
        Path(__file__).parents[2] / "docs/public-demo/dicom-deidentification-policy.json"
    )
    policy = json.loads(policy_path.read_text(encoding="utf-8"))
    policy["approval"] = {
        "status": "approved",
        "approved_by": "Test Reviewer",
        "approved_at": "2026-08-19T00:00:00Z",
    }
    policy["require_pixel_review_attestation"] = False
    return policy


def dicom_bytes(**overrides):
    meta = FileMetaDataset()
    meta.TransferSyntaxUID = ExplicitVRLittleEndian
    meta.MediaStorageSOPClassUID = generate_uid()
    meta.MediaStorageSOPInstanceUID = generate_uid()
    dataset = FileDataset(None, {}, file_meta=meta, preamble=b"\0" * 128)
    dataset.SOPClassUID = meta.MediaStorageSOPClassUID
    dataset.SOPInstanceUID = meta.MediaStorageSOPInstanceUID
    dataset.StudyInstanceUID = generate_uid()
    dataset.SeriesInstanceUID = generate_uid()
    dataset.PatientID = "DEMO-ABC123"
    dataset.PatientName = ""
    dataset.PatientIdentityRemoved = "YES"
    dataset.DeidentificationMethod = "Test profile"
    dataset.BurnedInAnnotation = "NO"
    for keyword, value in overrides.items():
        if value is None:
            delattr(dataset, keyword)
        else:
            setattr(dataset, keyword, value)
    with tempfile.SpooledTemporaryFile() as handle:
        dataset.save_as(handle, enforce_file_format=True)
        handle.seek(0)
        return handle.read()


class DemoDicomValidatorTests(unittest.TestCase):
    def test_safe_dataset_passes_with_approved_policy(self):
        policy = approved_policy()
        state = validator.scan_instances([validator.InstanceInput("safe", dicom_bytes())], policy)
        report = validator.build_report(policy, state, "test", None)
        self.assertEqual("PASS", report["result"])
        self.assertEqual(0, report["summary"]["violations"])
        self.assertEqual(1, report["dataset"]["instances"])

    def test_populated_patient_name_fails_without_disclosing_value(self):
        policy = approved_policy()
        secret_name = "Sensitive^Person"
        state = validator.scan_instances(
            [validator.InstanceInput("unsafe", dicom_bytes(PatientName=secret_name))], policy
        )
        report = validator.build_report(policy, state, "test", None)
        self.assertEqual("FAIL", report["result"])
        self.assertIn("prohibited_tag_populated", report["summary"]["violations_by_code"])
        self.assertNotIn(secret_name, json.dumps(report))
        self.assertNotIn(secret_name, validator.markdown_report(report))

    def test_patient_id_is_required_and_must_be_nonempty(self):
        policy = approved_policy()
        for patient_id, expected_code in (
            (None, "required_tag_missing"),
            ("", "required_value_empty"),
        ):
            with self.subTest(patient_id=patient_id):
                state = validator.scan_instances(
                    [validator.InstanceInput("unsafe", dicom_bytes(PatientID=patient_id))],
                    policy,
                )
                report = validator.build_report(policy, state, "test", None)
                self.assertEqual("FAIL", report["result"])
                self.assertIn(expected_code, report["summary"]["violations_by_code"])

    def test_private_element_fails(self):
        policy = approved_policy()
        payload = dicom_bytes()
        with tempfile.SpooledTemporaryFile() as handle:
            handle.write(payload)
            handle.seek(0)
            dataset = validator.pydicom.dcmread(handle)
        dataset.add_new((0x0011, 0x0010), "LO", "PRIVATE_CREATOR")
        with tempfile.SpooledTemporaryFile() as handle:
            dataset.save_as(handle, enforce_file_format=True)
            handle.seek(0)
            payload = handle.read()
        state = validator.scan_instances([validator.InstanceInput("private", payload)], policy)
        report = validator.build_report(policy, state, "test", None)
        self.assertEqual("FAIL", report["result"])
        self.assertIn("private_tag_present", report["summary"]["violations_by_code"])

    def test_pending_policy_is_incomplete(self):
        policy = approved_policy()
        policy["approval"] = {"status": "pending", "approved_by": "", "approved_at": ""}
        state = validator.scan_instances([validator.InstanceInput("safe", dicom_bytes())], policy)
        report = validator.build_report(policy, state, "test", None)
        self.assertEqual("INCOMPLETE", report["result"])

    def test_invalid_approval_timestamp_is_incomplete(self):
        policy = approved_policy()
        policy["approval"]["approved_at"] = "not-a-date"
        state = validator.scan_instances([validator.InstanceInput("safe", dicom_bytes())], policy)
        report = validator.build_report(policy, state, "test", None)
        self.assertEqual("INCOMPLETE", report["result"])

    def test_violation_is_fail_even_when_policy_is_pending(self):
        policy = approved_policy()
        policy["approval"] = {"status": "pending", "approved_by": "", "approved_at": ""}
        state = validator.scan_instances(
            [validator.InstanceInput("unsafe", dicom_bytes(PatientName="Sensitive^Person"))],
            policy,
        )
        report = validator.build_report(policy, state, "test", None)
        self.assertEqual("FAIL", report["result"])
        self.assertTrue(report["blockers"])

    def test_pixel_attestation_must_match_dataset(self):
        policy = approved_policy()
        policy["require_pixel_review_attestation"] = True
        state = validator.scan_instances([validator.InstanceInput("safe", dicom_bytes())], policy)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "attestation.json"
            path.write_text(
                json.dumps(
                    {
                        "status": "passed",
                        "reviewer": "Test Reviewer",
                        "reviewed_at": "2026-08-19T00:00:00Z",
                        "dataset_fingerprint": "wrong",
                    }
                ),
                encoding="utf-8",
            )
            report = validator.build_report(policy, state, "test", path)
        self.assertEqual("INCOMPLETE", report["result"])
        self.assertEqual("stale", report["pixel_review"]["status"])

    def test_parse_error_prevents_pass(self):
        policy = approved_policy()
        state = validator.scan_instances(
            [validator.InstanceInput("broken", b"not a dicom")], policy
        )
        report = validator.build_report(policy, state, "test", None)
        self.assertEqual("INCOMPLETE", report["result"])
        self.assertEqual(1, report["summary"]["validation_errors"])

    def test_explicit_extensionless_dicom_file_is_scanned(self):
        policy = approved_policy()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "instance"
            path.write_bytes(dicom_bytes())
            state = validator.scan_instances(
                validator.iter_local_instances(path, all_files=False), policy
            )
        report = validator.build_report(policy, state, "test", None)
        self.assertEqual("PASS", report["result"])
        self.assertEqual(1, report["dataset"]["instances"])

    def test_local_read_error_produces_redacted_incomplete_report(self):
        policy = approved_policy()
        sensitive_error = "permission denied: /sensitive/patient-name.dcm"
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "instance.dcm"
            path.write_bytes(b"placeholder")
            with patch.object(Path, "read_bytes", side_effect=PermissionError(sensitive_error)):
                state = validator.scan_instances(
                    validator.iter_local_instances(path, all_files=False), policy
                )
        report = validator.build_report(policy, state, "test", None)
        self.assertEqual("INCOMPLETE", report["result"])
        self.assertEqual(1, report["summary"]["validation_errors"])
        self.assertNotIn(sensitive_error, json.dumps(report))

    def test_orthanc_instance_download_error_does_not_abort_scan(self):
        policy = approved_policy()
        with patch.object(
            validator,
            "orthanc_request",
            side_effect=[b'["orthanc-instance-id"]', validator.urllib.error.URLError("secret")],
        ):
            state = validator.scan_instances(
                validator.iter_orthanc_instances("http://127.0.0.1:8042", "user", "password"),
                policy,
            )
        report = validator.build_report(policy, state, "test", None)
        self.assertEqual("INCOMPLETE", report["result"])
        self.assertEqual(1, report["summary"]["validation_errors"])
        self.assertNotIn("secret", json.dumps(report))
        self.assertNotIn("orthanc-instance-id", json.dumps(report))


if __name__ == "__main__":
    unittest.main()
