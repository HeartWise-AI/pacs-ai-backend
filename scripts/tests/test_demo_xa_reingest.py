from __future__ import annotations

# DICOM DA/TM values are deliberately represented as naive local wall times.
# ruff: noqa: DTZ001
import importlib.util
import json
import sys
import tempfile
import unittest
from datetime import datetime, timedelta
from pathlib import Path
from unittest.mock import Mock, patch

import pydicom
from pydicom.dataset import FileDataset, FileMetaDataset
from pydicom.uid import ExplicitVRLittleEndian, SecondaryCaptureImageStorage, generate_uid

SCRIPT_PATH = Path(__file__).resolve().parents[1] / "demo_xa_reingest.py"
SPEC = importlib.util.spec_from_file_location("demo_xa_reingest", SCRIPT_PATH)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def write_xa(path: Path, *, study_uid: str, series_uid: str, sop_uid: str, when: datetime) -> bytes:
    file_meta = FileMetaDataset()
    file_meta.MediaStorageSOPClassUID = SecondaryCaptureImageStorage
    file_meta.MediaStorageSOPInstanceUID = sop_uid
    file_meta.TransferSyntaxUID = ExplicitVRLittleEndian
    dataset = FileDataset(str(path), {}, file_meta=file_meta, preamble=b"\0" * 128)
    dataset.SOPClassUID = SecondaryCaptureImageStorage
    dataset.SOPInstanceUID = sop_uid
    dataset.StudyInstanceUID = study_uid
    dataset.SeriesInstanceUID = series_uid
    dataset.Modality = "XA"
    dataset.PatientID = "ORIGINAL"
    dataset.PatientName = "Demo^Original"
    dataset.StudyDate = when.strftime("%Y%m%d")
    dataset.StudyTime = when.strftime("%H%M%S")
    dataset.SeriesDate = when.strftime("%Y%m%d")
    dataset.SeriesTime = when.strftime("%H%M%S")
    dataset.Rows = 2
    dataset.Columns = 2
    dataset.SamplesPerPixel = 1
    dataset.PhotometricInterpretation = "MONOCHROME2"
    dataset.BitsAllocated = 8
    dataset.BitsStored = 8
    dataset.HighBit = 7
    dataset.PixelRepresentation = 0
    dataset.PixelData = b"\x01\x02\x03\x04"
    dataset.save_as(path, enforce_file_format=True)
    return dataset.PixelData


def config(root: Path, seed: Path) -> MODULE.Config:
    return MODULE.Config(
        api_base_url="http://127.0.0.1:8085",
        tenant_id="tenant",
        admin_email="admin@example.test",
        admin_password="secret",
        turnstile_token="",
        seed_dir=seed,
        source_orthanc_url="http://127.0.0.1:8063",
        source_orthanc_user="",
        source_orthanc_password="",
        destination_orthanc_url="http://127.0.0.1:8042",
        destination_orthanc_user="",
        destination_orthanc_password="",
        study_service_url="http://127.0.0.1:8600",
        study_service_token="",
        work_dir=root / "runs",
        timezone_name="UTC",
        poll_seconds=0,
        drain_timeout_seconds=1,
        processing_timeout_seconds=1,
        stable_timeout_seconds=1,
        compose_file=root / "docker-compose.yml",
        patient_id_prefix="DEMO-XA",
    )


class DicomReplayTests(unittest.TestCase):
    def test_rewrites_identifiers_and_time_without_changing_pixels(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            seed = root / "seed"
            seed.mkdir()
            study_uid = generate_uid()
            first_series = generate_uid()
            second_series = generate_uid()
            first_time = datetime(2020, 1, 2, 10, 0, 0)
            original_pixels = write_xa(
                seed / "a.dcm",
                study_uid=study_uid,
                series_uid=first_series,
                sop_uid=generate_uid(),
                when=first_time,
            )
            write_xa(
                seed / "b.dcm",
                study_uid=study_uid,
                series_uid=second_series,
                sop_uid=generate_uid(),
                when=first_time + timedelta(seconds=9),
            )

            inventory = MODULE.inspect_seed(seed)
            target = datetime(2026, 9, 23, 12, 0, 0)
            replay = MODULE.build_replay(
                inventory, root / "out", target_time=target, patient_id="DEMO-XA-TEST"
            )
            rewritten = [pydicom.dcmread(root / "out" / name) for name in replay["files"]]

            self.assertNotEqual(replay["study_uid"], study_uid)
            self.assertEqual({str(item.StudyInstanceUID) for item in rewritten}, {replay["study_uid"]})
            self.assertEqual(len({str(item.SeriesInstanceUID) for item in rewritten}), 2)
            self.assertEqual({str(item.PatientID) for item in rewritten}, {"DEMO-XA-TEST"})
            self.assertEqual({str(item.PatientIdentityRemoved) for item in rewritten}, {"YES"})
            self.assertEqual(rewritten[0].PixelData, original_pixels)
            rewritten_times = [
                MODULE.parse_dicom_datetime(item.SeriesDate, item.SeriesTime) for item in rewritten
            ]
            self.assertEqual(rewritten_times[1] - rewritten_times[0], timedelta(seconds=9))
            self.assertEqual(max(rewritten_times), target)

    def test_seed_must_be_one_xa_study(self):
        with tempfile.TemporaryDirectory() as temporary:
            seed = Path(temporary)
            write_xa(
                seed / "one.dcm",
                study_uid=generate_uid(),
                series_uid=generate_uid(),
                sop_uid=generate_uid(),
                when=datetime(2020, 1, 1),
            )
            write_xa(
                seed / "two.dcm",
                study_uid=generate_uid(),
                series_uid=generate_uid(),
                sop_uid=generate_uid(),
                when=datetime(2020, 1, 1),
            )
            with self.assertRaisesRegex(MODULE.ReingestionError, "exactly one XA study"):
                MODULE.inspect_seed(seed)


class RoutingTests(unittest.TestCase):
    def job(self, name: str = "DeepCORO-MACE", version: str = "1.0.0") -> dict:
        return {"id": "job-1", "modelName": name, "modelVersion": version}

    def snapshot(self) -> dict:
        return {
            "registry": {
                "DeepCORO-MACE": {"version": "1.0.0", "queue": "deepcoro-mace"}
            },
            "active_queues": {"worker": [{"name": "deepcoro-mace"}]},
        }

    def test_accepts_exact_route_with_live_worker(self):
        self.assertEqual(
            MODULE.validate_routes([self.job()], self.snapshot()),
            {"job-1": "deepcoro-mace"},
        )

    def test_rejects_name_version_and_worker_mismatches(self):
        with self.assertRaisesRegex(MODULE.ReingestionError, "not routable"):
            MODULE.validate_routes([self.job(name="DeepCORO_MACE")], self.snapshot())
        with self.assertRaisesRegex(MODULE.ReingestionError, "registry version"):
            MODULE.validate_routes([self.job(version="2.0.0")], self.snapshot())
        snapshot = self.snapshot()
        snapshot["active_queues"] = {}
        with self.assertRaisesRegex(MODULE.ReingestionError, "no live Celery worker"):
            MODULE.validate_routes([self.job()], snapshot)

    def test_active_window_rejects_expired_or_incompatible_jobs(self):
        now = datetime(2026, 9, 23, 12, 0, 0)
        job = {
            **self.job(),
            "status": "RUNNING",
            "scheduleEndTimestamp": int(now.timestamp()) - 1,
            "studyTimeStart": "130000",
            "studyTimeEnd": "140000",
            "stabilityMinutes": 10,
            "recentWindowMinutes": 10,
        }
        with self.assertRaisesRegex(MODULE.ReingestionError, "active-window") as raised:
            MODULE.validate_active_job_windows([job], now)
        self.assertIn("schedule has ended", str(raised.exception))
        self.assertIn("outside its study-time filter", str(raised.exception))
        self.assertIn("stability window", str(raised.exception))


class StateMachineTests(unittest.TestCase):
    def make_runner(self, root: Path, *, prune: bool = True) -> MODULE.DemoXaReingestion:
        seed = root / "seed"
        seed.mkdir()
        return MODULE.DemoXaReingestion(
            config(root, seed),
            execute=True,
            prune_previous=prune,
            allow_database_cleanup=prune,
            api=Mock(),
            source=Mock(),
            destination=Mock(),
            study=Mock(),
            runtime=Mock(),
            sleeper=lambda _: None,
        )

    @staticmethod
    def preflight_data():
        jobs = [
            {
                "id": "job-1",
                "modelName": "DeepCORO-MACE",
                "modelVersion": "1.0.0",
                "status": "RUNNING",
            }
        ]
        inventory = MODULE.SeedInventory(
            files=(Path("unused.dcm"),),
            study_uid="1.2.3",
            series_count=1,
            instance_count=1,
            latest_timestamp=datetime(2020, 1, 1),
        )
        return jobs, {"job-1": "deepcoro-mace"}, inventory, "1.2.3"

    def test_failure_restores_jobs_and_never_cleans_previous(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.make_runner(Path(temporary))
            runner._preflight = Mock(return_value=self.preflight_data())
            runner._pause = Mock(return_value=["job-1"])
            runner._restore = Mock()
            runner._cleanup_previous = Mock()
            with (
                patch.object(MODULE, "build_replay", side_effect=MODULE.ReingestionError("boom")),
                self.assertRaisesRegex(MODULE.ReingestionError, "boom"),
            ):
                runner.run()
            runner._restore.assert_called_once_with(["job-1"])
            runner._cleanup_previous.assert_not_called()
            manifests = list((Path(temporary) / "runs").glob("*/manifest.json"))
            self.assertEqual(len(manifests), 1)
            self.assertEqual(json.loads(manifests[0].read_text())["status"], "failed")

    def test_pause_failure_still_restores_original_running_jobs(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.make_runner(Path(temporary))
            runner._preflight = Mock(return_value=self.preflight_data())
            runner._pause = Mock(side_effect=MODULE.ReingestionError("drain failed"))
            runner._restore = Mock()
            with self.assertRaisesRegex(MODULE.ReingestionError, "drain failed"):
                runner.run()
            runner._restore.assert_called_once_with(["job-1"])

    def test_cleanup_only_runs_after_all_results_succeed(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.make_runner(Path(temporary))
            runner._preflight = Mock(return_value=self.preflight_data())
            runner._pause = Mock(return_value=["job-1"])
            runner._restore = Mock()
            runner._upload = Mock(return_value="source-id")
            runner._monitor = Mock(return_value={"job-1": {"status": "completed"}})
            runner._cleanup_previous = Mock(return_value={"status": "deleted"})
            replay = {
                "study_uid": "9.8.7",
                "uid_mapping": {"1.2.3": "9.8.7"},
                "files": ["instance-0001.dcm"],
                "shift_seconds": 1,
            }
            with patch.object(MODULE, "build_replay", return_value=replay):
                self.assertEqual(runner.run(), 0)
            runner._restore.assert_called_once_with(["job-1"])
            runner._monitor.assert_called_once()
            runner._cleanup_previous.assert_called_once_with("1.2.3")
            latest = json.loads(runner.latest_path.read_text())
            self.assertEqual(latest["status"], "succeeded")

    def test_cleanup_database_is_explicitly_gated(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.make_runner(Path(temporary), prune=False)
            runner.source.find_studies.return_value = []
            runner.destination.find_studies.return_value = []
            result = runner._cleanup_previous("1.2.3")
            self.assertEqual(result["database_rows"], "retained")
            runner.runtime.cleanup_database_rows.assert_not_called()

    def test_database_cleanup_rejects_untrusted_identifiers(self):
        runtime = MODULE.ComposeRuntime(Path("docker-compose.yml"), runner=Mock())
        with self.assertRaisesRegex(MODULE.ReingestionError, "invalid DICOM UID"):
            runtime.cleanup_database_rows("1.2.3'; DROP TABLE pipeline_jobs; --", "tenant")
        with self.assertRaisesRegex(MODULE.ReingestionError, "invalid tenant ID"):
            runtime.cleanup_database_rows("1.2.3", "tenant'; DROP TABLE pipeline_jobs; --")


class DryRunTests(unittest.TestCase):
    def test_dry_run_performs_no_mutating_step(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            seed = root / "seed"
            seed.mkdir()
            runner = MODULE.DemoXaReingestion(
                config(root, seed),
                execute=False,
                prune_previous=False,
                allow_database_cleanup=False,
                api=Mock(),
                source=Mock(),
                destination=Mock(),
                study=Mock(),
                runtime=Mock(),
            )
            runner._preflight = Mock(return_value=StateMachineTests.preflight_data())
            runner._pause = Mock()
            runner._cleanup_previous = Mock()
            self.assertEqual(runner.run(), 0)
            runner._pause.assert_not_called()
            runner._cleanup_previous.assert_not_called()
            self.assertFalse(runner.config.work_dir.exists())


if __name__ == "__main__":
    unittest.main()
