"""Check RET-02 provenance, inventories and real runtime evidence bindings."""
import hashlib
import json
import os
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / 'tests/fixtures/phase4a-evidence/ret-02'
SEM = ['SEM-01', 'SEM-02', 'SEM-03', 'SEM-06']
SOURCE = '166262365d506157b99968105c890f480cc7c07c'
PLAN = '7eefdbf50d3596890a75e97a400dc1f5286e6498'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def read(name):
    return json.loads((EVIDENCE / name).read_text(encoding='utf-8'))


class Ret02EvidenceTests(unittest.TestCase):
    def test_inventory_is_complete_and_byte_bound(self):
        manifest = read('evidence-manifest.json')
        self.assertEqual(manifest['caseID'], 'RET-02')
        actual = {p.relative_to(EVIDENCE).as_posix() for p in EVIDENCE.rglob('*')
                  if p.is_file() and p.name not in ('README.md', 'evidence-manifest.json')}
        self.assertEqual(set(manifest['inventory']), actual)
        for name, item in manifest['inventory'].items():
            path = EVIDENCE / name
            self.assertEqual(path.stat().st_size, item['size'], name)
            self.assertEqual(digest(path), item['sha256'], name)

    def test_independent_exact_source_generation(self):
        generation = read('source-generation/record.json')
        provenance = read('source-provenance.json')
        self.assertEqual(generation['caseID'], 'RET-02')
        self.assertEqual(provenance['sourceRecord'], 'ret-02/source-generation')
        self.assertEqual(generation['SourceRelease'], 'v1.14.4')
        self.assertEqual(generation['SourceCommit'], SOURCE)
        self.assertEqual(generation['SourceProducerBuildCodeCommit'], SOURCE)
        self.assertEqual(generation['PlanCommit'], PLAN)
        self.assertTrue(generation['generationSuccess'])
        self.assertTrue(generation['process']['reaped'])
        self.assertEqual(generation['process']['exitCode'], 0)
        self.assertEqual(generation['process']['deliveredPayloads'], 5)
        self.assertEqual(generation['process']['expectedUniqueEvents'], 4)
        self.assertEqual(generation['ControlledInputSHA256'], digest(EVIDENCE / 'profile.json'))
        ret01 = json.loads((EVIDENCE.parent / 'ret-01/source-provenance.json').read_text())
        self.assertNotEqual(generation['GeneratedBundleSHA256'], ret01['sourceBundleDigest'])
        self.assertEqual(provenance['sourceBundleDigest'], generation['GeneratedBundleSHA256'])
        self.assertEqual(provenance['sourceGenerationRecordSHA256'], digest(EVIDENCE / 'source-generation/record.json'))
        for name, sha in generation['GenerationEvidenceFiles'].items():
            if name == 'producer.log':
                self.assertEqual(provenance['externalGenerationFiles'][name], sha)
            else:
                self.assertEqual(digest(EVIDENCE / 'source-generation' / name), sha, name)
        runner = read('generation-runner.json')
        self.assertEqual(runner['codeCommit'], generation['GeneratorCodeCommit'])
        self.assertEqual(runner['generationRecordSHA256'], provenance['sourceGenerationRecordSHA256'])
        self.assertEqual(runner['executionExitCode'], 0)

    def test_real_archive_and_raw_coexistence(self):
        flow = read('source-generation/archive-flow.json')
        self.assertFalse(flow['deleteCalled'])
        self.assertFalse(flow['automaticRetentionEnabled'])
        self.assertEqual(flow['verify']['run']['status'], 'verified')
        archive = read('source-generation/archive-inventory.json')
        self.assertTrue(archive['authoritativeArchiveRawEqual'])
        self.assertEqual(archive['rawEventCount'], 4)
        self.assertEqual(archive['archiveEventCount'], 4)
        self.assertEqual(archive['rawDeletedEventCount'], 0)
        self.assertEqual(len(archive['archiveRecords']), 4)
        self.assertEqual(len(archive['refs']), 4)
        self.assertTrue(archive['segments'])
        self.assertTrue(all(ref['raw_deleted_at_ms'] is None for ref in archive['refs']))
        self.assertEqual(digest(EVIDENCE / 'source-generation/pre-archive-snapshot.json'),
                         digest(EVIDENCE / 'source-generation/source-snapshot.json'))

    def test_platform_runtime_sem_and_recovery(self):
        bundle = read('source-provenance.json')['sourceBundleDigest']
        names = {'inputBundle': 'input-bundle', 'semanticAssertions': 'semantic-assertions',
                 'executionRecord': 'execution-record', 'backupRestore': 'backup-restore',
                 'rawEventInventory': 'raw-event-inventory', 'archiveInventory': 'archive-inventory'}
        for prefix in ('runtime', 'platform-replay/linux'):
            result = read(prefix + '/result.json')
            runner = read(prefix + '/runner-evidence.json')
            self.assertEqual(result['runtimeEvidence'], 'pass')
            self.assertEqual(result['inputBundleDigest'], bundle)
            self.assertEqual(result['authoritativeBeforeDigest'], result['authoritativeAfterDigest'])
            self.assertEqual(result['applicableSEM'], SEM)
            self.assertEqual(result['backupRestore'], 'pass')
            self.assertEqual(result['rawDeletedEventCount'], 0)
            self.assertEqual(runner['codeCommit'], result['executionCommit'])
            self.assertEqual(runner['resultSHA256'], digest(EVIDENCE / prefix / 'result.json'))
            self.assertEqual(runner['executionExitCode'], 0)
            for key, name in names.items():
                self.assertEqual(result['evidence'][key], digest(EVIDENCE / prefix / (name + '.json')))
            assertions = read(prefix + '/semantic-assertions.json')['assertions']
            for sem in SEM:
                self.assertEqual(assertions[sem]['status'], 'runtime-pass')
            for imported in assertions['SEM-02']['archiveImports']:
                self.assertEqual(imported['added'], 0)
                self.assertEqual(imported['skipped'], 4)
                self.assertEqual(imported['failed'], 0)
            self.assertTrue(assertions['SEM-02']['after']['retainedProjectionRawParity'])
            recovery = read(prefix + '/backup-restore.json')
            self.assertTrue(recovery['newRoot'])
            self.assertTrue(recovery['inventoryMatched'])
            self.assertTrue(recovery['archiveRawParity'])
            self.assertEqual(recovery['backupManifestDigest'], recovery['restoredManifestDigest'])
            self.assertEqual(recovery['queries']['retainedPricingCalls'], 4)

    def test_acceptance_and_upgrade_authority_remain_closed(self):
        for name in ('runtime/result.json', 'platform-replay/linux/result.json', 'source-provenance.json'):
            result = read(name)
            self.assertFalse(result['casePass'])
            self.assertEqual(result['independentAcceptance'], 'pending')
            self.assertFalse(result['UpgradeAuthorized'])
            self.assertFalse(result['TrustedHopReceipt'])
        result = read('runtime/result.json')
        self.assertFalse(result['Phase4A07Complete'])
        self.assertFalse(result['Phase4AComplete'])
        self.assertEqual(result['remainingCases'], {'count': 46, 'status': 'pending'})

    def test_clean_checkout_replay_binds_current_runtime_sources(self):
        clean = read('clean-checkout-replay.json')
        pin = read('source-provenance.json')['sourceArtifact']
        self.assertEqual(clean['materializedArchiveSHA256'], pin['archiveSHA256'])
        for platform in ('linux', 'macos'):
            execution = clean['platforms'][platform]
            self.assertEqual(execution['executionExitCode'], 0)
            self.assertTrue(execution['newRootRecovery'])
            self.assertEqual(execution['runtimeResult']['runtimeEvidence'], 'pass')
            self.assertEqual(execution['runtimeResult']['inputBundleDigest'], pin['sourceBundleDigest'])
            for name, sha in execution['runtimeSourceFiles'].items():
                self.assertEqual(digest(ROOT / name), sha, name)

    def test_immutable_asset_pins_bind_original_bytes(self):
        provenance = read('source-provenance.json')
        pin = provenance['sourceArtifact']
        self.assertTrue(pin['immutableRelease'])
        self.assertEqual(pin['repository'], 'seakee/CPA-Manager-Plus')
        self.assertTrue(pin['tag'].startswith('qualification-ret02-'))
        self.assertGreater(pin['releaseID'], 0)
        self.assertGreater(pin['assetID'], 0)
        self.assertEqual(pin['sourceBundleDigest'], provenance['sourceBundleDigest'])
        self.assertEqual(pin['generationRecordSHA256'], digest(EVIDENCE / 'source-generation/record.json'))
        self.assertEqual(pin['inventory']['producer.tar.gz']['sha256'], provenance['sourceProducerArchiveSHA256'])
        self.assertEqual(pin['archiveSHA256'], 'b8a49362108d5a3281bd4de2d5cde0f3b3658d6f348b014bc7f95a34a74baa25')
        self.assertEqual(pin['archiveSize'], 8828354)

    @unittest.skipUnless(os.environ.get('CPAMP_RET02_FULL_EVIDENCE'), 'opt-in original immutable bytes')
    def test_original_fixture_and_backup_bytes(self):
        root = Path(os.environ['CPAMP_RET02_FULL_EVIDENCE'])
        inventory = read('source-provenance.json')['sourceArtifact']['inventory']
        self.assertEqual(set(inventory), {p.relative_to(root).as_posix() for p in root.rglob('*') if p.is_file()})
        for name, item in inventory.items():
            path = root / name
            self.assertEqual(path.stat().st_size, item['size'], name)
            self.assertEqual(digest(path), item['sha256'], name)
        self.assertEqual((root / 'ret-02/source-generation/record.json').read_bytes(),
                         (EVIDENCE / 'source-generation/record.json').read_bytes())
        for platform in ('linux', 'macos'):
            backup = root / 'replay' / platform / 'runtime/backup-restore/bundle'
            manifest = json.loads((backup / 'manifest.json').read_text())
            for name, entry in manifest['entries'].items():
                if entry['present'] and not entry.get('directory'):
                    self.assertEqual(digest(backup / name), entry['sha256'], name)


if __name__ == '__main__':
    unittest.main()
