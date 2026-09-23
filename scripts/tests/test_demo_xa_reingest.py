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


def config(root: Path) -> MODULE.Config:
    return MODULE.Config(
        api_base_url="http://127.0.0.1:8085",
        tenant_id="tenant",
        admin_email="admin@example.test",
        admin_password="secret",
        turnstile_token="",
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

            inventory = MODULE.inspect_study_files(seed)
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

    def test_each_snapshot_must_be_one_xa_study(self):
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
                MODULE.inspect_study_files(seed)


class OrthancInventoryTests(unittest.TestCase):
    def client(self, responses: dict[str, object]) -> MODULE.OrthancClient:
        client = MODULE.OrthancClient("http://127.0.0.1:8063")
        client.http = Mock()
        client.http.request.side_effect = lambda _method, path: responses[path]
        return client

    def test_discovers_every_xa_study_and_counts_series_and_instances(self):
        responses = {
            "/studies": ["study-b", "study-a", "study-ct"],
            "/studies/study-a": {
                "MainDicomTags": {"StudyInstanceUID": "1.2.3"},
                "Series": ["series-a1", "series-a2"],
            },
            "/studies/study-b": {
                "MainDicomTags": {"StudyInstanceUID": "1.2.4"},
                "Series": ["series-b1"],
            },
            "/studies/study-ct": {
                "MainDicomTags": {"StudyInstanceUID": "1.2.5"},
                "Series": ["series-ct"],
            },
            "/series/series-a1": {
                "MainDicomTags": {"Modality": "XA"},
                "Instances": ["a1"],
            },
            "/series/series-a2": {
                "MainDicomTags": {"Modality": "XA"},
                "Instances": ["a2", "a3"],
            },
            "/series/series-b1": {
                "MainDicomTags": {"Modality": "XA"},
                "Instances": ["b1"],
            },
            "/series/series-ct": {
                "MainDicomTags": {"Modality": "CT"},
                "Instances": ["ct1"],
            },
        }

        studies = self.client(responses).xa_studies()

        self.assertEqual([item.study_uid for item in studies], ["1.2.3", "1.2.4"])
        self.assertEqual([item.series_count for item in studies], [2, 1])
        self.assertEqual([item.instance_count for item in studies], [3, 1])

    def test_rejects_mixed_modality_study_before_whole_study_cleanup(self):
        responses = {
            "/studies": ["mixed"],
            "/studies/mixed": {
                "MainDicomTags": {"StudyInstanceUID": "1.2.3"},
                "Series": ["xa", "ct"],
            },
            "/series/xa": {
                "MainDicomTags": {"Modality": "XA"},
                "Instances": ["xa1"],
            },
            "/series/ct": {
                "MainDicomTags": {"Modality": "CT"},
                "Instances": ["ct1"],
            },
        }

        with self.assertRaisesRegex(MODULE.ReingestionError, "unsafe whole-study cleanup"):
            self.client(responses).xa_studies()

    def test_download_validates_the_source_snapshot(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source_file = root / "source.dcm"
            write_xa(
                source_file,
                study_uid="1.2.3",
                series_uid="1.2.3.4",
                sop_uid="1.2.3.4.5",
                when=datetime(2020, 1, 1),
            )
            client = MODULE.OrthancClient("http://127.0.0.1:8063")
            client.http = Mock()
            client.http.request_bytes.return_value = source_file.read_bytes()
            source_study = MODULE.XaStudy("orthanc-study", "1.2.3", 1, ("instance",))

            inventory = client.download_study(source_study, root / "snapshot")

            self.assertEqual(inventory.study_uid, "1.2.3")
            self.assertEqual(inventory.instance_count, 1)


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
        return MODULE.DemoXaReingestion(
            config(root),
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
        current_studies = (
            MODULE.XaStudy("orthanc-1", "1.2.3", 1, ("instance-1",)),
            MODULE.XaStudy("orthanc-2", "1.2.4", 1, ("instance-2",)),
        )
        return jobs, {"job-1": "deepcoro-mace"}, current_studies

    @staticmethod
    def study_inventory(study_uid: str) -> MODULE.StudyInventory:
        return MODULE.StudyInventory(
            files=(Path("unused.dcm"),),
            study_uid=study_uid,
            series_count=1,
            instance_count=1,
            latest_timestamp=datetime(2020, 1, 1),
        )

    def test_preflight_inventories_the_website_facing_orthanc(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.make_runner(Path(temporary))
            job = {
                "id": "job-1",
                "containerId": "container-1",
                "modelName": "DeepCORO-MACE",
                "modelVersion": "1.0.0",
                "modalities": ["XA"],
                "status": "RUNNING",
            }
            runner.api.ingestion_jobs.return_value = [job]
            runner.runtime.snapshot.return_value = {
                "registry": {
                    "DeepCORO-MACE": {"version": "1.0.0", "queue": "deepcoro-mace"}
                },
                "active_queues": {"worker": [{"name": "deepcoro-mace"}]},
            }
            studies = self.preflight_data()[2]
            runner.destination.xa_studies.return_value = studies

            self.assertEqual(runner._preflight(), ([job], {"job-1": "deepcoro-mace"}, studies))

            runner.destination.xa_studies.assert_called_once_with()
            runner.source.xa_studies.assert_not_called()

    def test_failure_restores_jobs_and_never_cleans_previous(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.make_runner(Path(temporary))
            runner._preflight = Mock(return_value=self.preflight_data())
            runner._pause = Mock(return_value=["job-1"])
            runner._restore = Mock()
            runner._cleanup_previous = Mock()
            runner.destination.xa_studies.return_value = self.preflight_data()[2]
            runner.destination.download_study.return_value = self.study_inventory("1.2.3")
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
            jobs, routes, current_studies = self.preflight_data()
            runner._preflight = Mock(return_value=(jobs, routes, current_studies))
            runner._pause = Mock(return_value=["job-1"])
            runner._restore = Mock()
            runner._upload = Mock(side_effect=["source-id-1", "source-id-2"])
            runner._wait_for_uploaded_studies = Mock()
            runner._monitor = Mock(return_value=[{"models": {"job-1": {"status": "completed"}}}])
            runner._cleanup_previous = Mock(return_value={"status": "deleted"})
            runner.destination.xa_studies.return_value = current_studies
            runner.destination.download_study.side_effect = [
                self.study_inventory("1.2.3"),
                self.study_inventory("1.2.4"),
            ]
            replays = [
                {
                    "study_uid": "9.8.7",
                    "uid_mapping": {"1.2.3": "9.8.7"},
                    "files": ["instance-0001.dcm"],
                    "shift_seconds": 1,
                },
                {
                    "study_uid": "9.8.8",
                    "uid_mapping": {"1.2.4": "9.8.8"},
                    "files": ["instance-0001.dcm"],
                    "shift_seconds": 1,
                },
            ]
            with patch.object(MODULE, "build_replay", side_effect=replays):
                self.assertEqual(runner.run(), 0)
            runner._restore.assert_called_once_with(["job-1"])
            runner._monitor.assert_called_once_with(jobs, ["9.8.7", "9.8.8"])
            self.assertEqual(
                [call.args[0] for call in runner._cleanup_previous.call_args_list],
                ["1.2.3", "1.2.4"],
            )
            latest = json.loads(runner.latest_path.read_text())
            self.assertEqual(latest["status"], "succeeded")
            self.assertEqual(len(latest["replays"]), 2)

    def test_failure_in_any_model_study_result_keeps_every_original(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.make_runner(Path(temporary))
            runner._preflight = Mock(return_value=self.preflight_data())
            runner._pause = Mock(return_value=["job-1"])
            runner._restore = Mock()
            runner._upload = Mock(side_effect=["source-id-1", "source-id-2"])
            runner._wait_for_uploaded_studies = Mock()
            runner._monitor = Mock(side_effect=MODULE.ReingestionError("model failed"))
            runner._cleanup_previous = Mock()
            runner.destination.xa_studies.return_value = self.preflight_data()[2]
            runner.destination.download_study.side_effect = [
                self.study_inventory("1.2.3"),
                self.study_inventory("1.2.4"),
            ]
            replays = [
                {
                    "study_uid": "9.8.7",
                    "uid_mapping": {"1.2.3": "9.8.7"},
                    "files": ["instance-0001.dcm"],
                    "shift_seconds": 1,
                },
                {
                    "study_uid": "9.8.8",
                    "uid_mapping": {"1.2.4": "9.8.8"},
                    "files": ["instance-0001.dcm"],
                    "shift_seconds": 1,
                },
            ]

            with (
                patch.object(MODULE, "build_replay", side_effect=replays),
                self.assertRaisesRegex(MODULE.ReingestionError, "model failed"),
            ):
                runner.run()

            runner._cleanup_previous.assert_not_called()

    def test_inventory_change_after_drain_aborts_before_snapshot_or_cleanup(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.make_runner(Path(temporary))
            jobs, routes, current_studies = self.preflight_data()
            runner._preflight = Mock(return_value=(jobs, routes, current_studies))
            runner._pause = Mock(return_value=["job-1"])
            runner._restore = Mock()
            runner._cleanup_previous = Mock()
            runner.destination.xa_studies.return_value = current_studies[:1]

            with self.assertRaisesRegex(MODULE.ReingestionError, "inventory changed"):
                runner.run()

            runner.destination.download_study.assert_not_called()
            runner._cleanup_previous.assert_not_called()
            runner._restore.assert_called_once_with(["job-1"])

    def test_monitor_requires_completion_for_every_study_and_model(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.make_runner(Path(temporary), prune=False)
            jobs = self.preflight_data()[0]
            runner.api.candidate.side_effect = lambda job_id, study_uid: {
                "id": f"candidate-{job_id}-{study_uid}",
                "status": "SUCCESS",
                "processingStatus": "completed",
            }
            runner.study.jobs_by_candidate.side_effect = lambda candidate_id: [
                {
                    "job_id": f"pipeline-{candidate_id}",
                    "model_name": "DeepCORO-MACE",
                    "model_version": "1.0.0",
                    "status": "completed",
                }
            ]
            runner.study.job.return_value = {"result_json": {"score": 0.5}}

            results = runner._monitor(jobs, ["9.8.7", "9.8.8"])

            self.assertEqual(len(results), 2)
            self.assertEqual(runner.api.candidate.call_count, 2)
            self.assertTrue(all(len(item["models"]) == 1 for item in results))

    def test_uploaded_studies_are_waited_for_as_one_batch(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.make_runner(Path(temporary), prune=False)
            runner.source.study.side_effect = lambda orthanc_id: {
                "IsStable": True,
                "Instances": [f"instance-{orthanc_id}"],
            }

            runner._wait_for_uploaded_studies({"study-a": 1, "study-b": 1})

            self.assertEqual(
                [call.args[0] for call in runner.source.study.call_args_list],
                ["study-a", "study-b"],
            )

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

    def test_database_cleanup_is_tenant_scoped_and_skips_unscoped_cache(self):
        calls = []

        def capture(command, **_kwargs):
            calls.append(command)
            return MODULE.subprocess.CompletedProcess(command, 0, stdout="", stderr="")

        runtime = MODULE.ComposeRuntime(Path("docker-compose.yml"), runner=capture)
        runtime.cleanup_database_rows("1.2.3", "tenant-a")

        self.assertEqual(len(calls), 2)
        study_sql = calls[0][-1]
        inference_sql = calls[1][-1]
        self.assertNotIn("study_preprocess_runs", study_sql)
        self.assertEqual(study_sql.count("tenant_id='tenant-a'"), 2)
        self.assertEqual(inference_sql.count("tenant_id='tenant-a'"), 2)


class DryRunTests(unittest.TestCase):
    def test_dry_run_performs_no_mutating_step(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            runner = MODULE.DemoXaReingestion(
                config(root),
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
