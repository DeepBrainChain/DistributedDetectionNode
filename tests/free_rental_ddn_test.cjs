/**
 * Free Rental DDN (Distributed Detection Node) Tests
 *
 * Tests for:
 *   1. dbc/dbc.go           — FreeRental contract methods
 *   2. ws/hub.go            — Offline handling for FreeRental machines
 *   3. http/contract.go     — HTTP endpoint FreeRental logic
 *   4. types/config.go      — Config struct FreeRental field
 *   5. dbc/free-rental/free_rental_abi.json — ABI definition
 *
 * Run: node tests/free_rental_ddn_test.cjs
 */

const fs = require('fs');
const path = require('path');
const assert = require('assert');

let passed = 0;
let failed = 0;
const failures = [];

function test(name, fn) {
  try {
    fn();
    passed++;
    console.log(`  PASS  ${name}`);
  } catch (err) {
    failed++;
    failures.push({ name, error: err.message });
    console.log(`  FAIL  ${name}`);
    console.log(`        ${err.message}`);
  }
}

// ─── Load source files ──────────────────────────────────────────
const rootDir = path.resolve(__dirname, '..');
const dbcGoSource = fs.readFileSync(path.join(rootDir, 'dbc', 'dbc.go'), 'utf8');
const hubGoSource = fs.readFileSync(path.join(rootDir, 'ws', 'hub.go'), 'utf8');
const contractGoSource = fs.readFileSync(path.join(rootDir, 'http', 'contract.go'), 'utf8');
const configGoSource = fs.readFileSync(path.join(rootDir, 'types', 'config.go'), 'utf8');
const abiPath = path.join(rootDir, 'dbc', 'free-rental', 'free_rental_abi.json');

// ═══════════════════════════════════════════════════════════════
// SECTION 1: dbc/dbc.go tests
// ═══════════════════════════════════════════════════════════════

console.log('\n=== dbc/dbc.go tests ===\n');

test('FreeRentalEnabled method exists', () => {
  assert.ok(dbcGoSource.includes('func (chain *dbcChain) FreeRentalEnabled() bool'),
    'FreeRentalEnabled method not found');
});

test('IsFreeRentalMachine method exists', () => {
  assert.ok(dbcGoSource.includes('func (chain *dbcChain) IsFreeRentalMachine(ctx context.Context, machineId string) (bool, error)'),
    'IsFreeRentalMachine method not found');
});

test('IsFreeRentalRented method exists', () => {
  assert.ok(dbcGoSource.includes('func (chain *dbcChain) IsFreeRentalRented(ctx context.Context, machineId string) (bool, error)'),
    'IsFreeRentalRented method not found');
});

test('NotifyFreeRental method exists', () => {
  assert.ok(dbcGoSource.includes('func (chain *dbcChain) NotifyFreeRental(ctx context.Context, tp uint8, machineId string) (string, error)'),
    'NotifyFreeRental method not found');
});

test('freeRental field in dbcChain struct', () => {
  assert.ok(dbcGoSource.includes('freeRental   *chainContract'),
    'freeRental field not found in dbcChain struct');
});

test('InitDbcChain handles FreeRentalContract config (optional)', () => {
  assert.ok(dbcGoSource.includes('config.FreeRentalContract.ContractAddress'),
    'should check FreeRentalContract config');
  assert.ok(dbcGoSource.includes('config.FreeRentalContract.AbiFile'),
    'should check AbiFile');
  assert.ok(dbcGoSource.includes('var freeRentalContract *chainContract'),
    'should declare freeRentalContract as nil-able');
});

test('InitDbcChain assigns freeRentalContract to chain struct', () => {
  assert.ok(dbcGoSource.includes('freeRental:   freeRentalContract'),
    'should assign freeRentalContract in struct initialization');
});

test('FreeRentalEnabled returns false when not configured (nil check)', () => {
  const methodIdx = dbcGoSource.indexOf('func (chain *dbcChain) FreeRentalEnabled()');
  const methodBlock = dbcGoSource.substring(methodIdx, methodIdx + 100);
  assert.ok(methodBlock.includes('chain.freeRental != nil'),
    'FreeRentalEnabled should check freeRental != nil');
});

test('IsFreeRentalMachine returns (false, nil) when not configured', () => {
  const methodIdx = dbcGoSource.indexOf('func (chain *dbcChain) IsFreeRentalMachine');
  const methodBlock = dbcGoSource.substring(methodIdx, methodIdx + 200);
  assert.ok(methodBlock.includes('chain.freeRental == nil'),
    'should check for nil freeRental');
  assert.ok(methodBlock.includes('return false, nil'),
    'should return (false, nil) when not configured');
});

test('IsFreeRentalMachine calls machines() view function', () => {
  const methodIdx = dbcGoSource.indexOf('func (chain *dbcChain) IsFreeRentalMachine');
  const methodBlock = dbcGoSource.substring(methodIdx, methodIdx + 1500);
  assert.ok(methodBlock.includes('.Pack("machines"'),
    'should call abi.Pack("machines")');
  assert.ok(methodBlock.includes('.Unpack("machines"'),
    'should unpack machines() result');
  assert.ok(methodBlock.includes('CallContract'),
    'should use CallContract (read-only)');
});

test('IsFreeRentalMachine extracts registered field (index 2)', () => {
  const methodIdx = dbcGoSource.indexOf('func (chain *dbcChain) IsFreeRentalMachine');
  const methodBlock = dbcGoSource.substring(methodIdx, methodIdx + 1500);
  assert.ok(methodBlock.includes('outputs[2].(bool)'),
    'should extract registered field at index 2');
});

test('IsFreeRentalRented calls machineIsRented() view function', () => {
  const methodIdx = dbcGoSource.indexOf('func (chain *dbcChain) IsFreeRentalRented');
  const methodBlock = dbcGoSource.substring(methodIdx, methodIdx + 1500);
  assert.ok(methodBlock.includes('.Pack("machineIsRented"'),
    'should call abi.Pack("machineIsRented")');
  assert.ok(methodBlock.includes('.Unpack("machineIsRented"'),
    'should unpack machineIsRented() result');
});

test('IsFreeRentalRented returns error when not configured', () => {
  const methodIdx = dbcGoSource.indexOf('func (chain *dbcChain) IsFreeRentalRented');
  const methodBlock = dbcGoSource.substring(methodIdx, methodIdx + 200);
  assert.ok(methodBlock.includes('FreeRental contract not configured'),
    'should return descriptive error when nil');
});

test('NotifyFreeRental calls notify() function with tp parameter', () => {
  const methodIdx = dbcGoSource.indexOf('func (chain *dbcChain) NotifyFreeRental');
  const methodBlock = dbcGoSource.substring(methodIdx, methodIdx + 600);
  assert.ok(methodBlock.includes('.Pack("notify", tp, machineId)'),
    'should pack notify with tp and machineId');
  assert.ok(methodBlock.includes('sendTx'),
    'should use sendTx (state-changing transaction)');
});

test('NotifyFreeRental uses freeRental contract for sendTx', () => {
  const methodIdx = dbcGoSource.indexOf('func (chain *dbcChain) NotifyFreeRental');
  const methodBlock = dbcGoSource.substring(methodIdx, methodIdx + 600);
  assert.ok(methodBlock.includes('chain.sendTx(ctx, chain.freeRental'),
    'should pass freeRental contract to sendTx');
});

// ═══════════════════════════════════════════════════════════════
// SECTION 2: ws/hub.go tests
// ═══════════════════════════════════════════════════════════════

console.log('\n=== ws/hub.go tests ===\n');

test('offlineFreeRental function exists', () => {
  assert.ok(hubGoSource.includes('func (do *delayOffline) offlineFreeRental(info delayOfflineChanInfo)'),
    'offlineFreeRental function not found');
});

test('offlineStaked function exists', () => {
  assert.ok(hubGoSource.includes('offlineStaked'),
    'offlineStaked function not found');
});

test('Offline function checks FreeRentalEnabled first', () => {
  const offlineIdx = hubGoSource.indexOf('func (do *delayOffline) Offline(info');
  const offlineBlock = hubGoSource.substring(offlineIdx, offlineIdx + 500);
  assert.ok(offlineBlock.includes('dbc.DbcChain.FreeRentalEnabled()'),
    'Offline should check FreeRentalEnabled');
});

test('Offline calls IsFreeRentalMachine to check machine type', () => {
  const offlineIdx = hubGoSource.indexOf('func (do *delayOffline) Offline(info');
  const offlineBlock = hubGoSource.substring(offlineIdx, offlineIdx + 500);
  assert.ok(offlineBlock.includes('dbc.DbcChain.IsFreeRentalMachine'),
    'Offline should call IsFreeRentalMachine');
});

test('Offline routes to offlineFreeRental when machine is FreeRental', () => {
  const offlineIdx = hubGoSource.indexOf('func (do *delayOffline) Offline(info');
  const offlineBlock = hubGoSource.substring(offlineIdx, offlineIdx + 1000);
  assert.ok(offlineBlock.includes('do.offlineFreeRental(info)'),
    'should route to offlineFreeRental');
  assert.ok(offlineBlock.includes('return'),
    'should return after offlineFreeRental');
});

test('Offline falls through to offlineStaked when not FreeRental', () => {
  const offlineIdx = hubGoSource.indexOf('func (do *delayOffline) Offline(info');
  const offlineEnd = hubGoSource.indexOf('}', hubGoSource.indexOf('do.offlineStaked(info)', offlineIdx));
  const offlineBlock = hubGoSource.substring(offlineIdx, offlineEnd + 10);
  assert.ok(offlineBlock.includes('do.offlineStaked(info)'),
    'should fall through to offlineStaked');
});

test('offlineFreeRental checks IsFreeRentalRented', () => {
  const fnIdx = hubGoSource.indexOf('func (do *delayOffline) offlineFreeRental');
  const fnBlock = hubGoSource.substring(fnIdx, fnIdx + 400);
  assert.ok(fnBlock.includes('dbc.DbcChain.IsFreeRentalRented'),
    'should check if machine is rented');
});

test('offlineFreeRental skips penalty when not rented', () => {
  const fnIdx = hubGoSource.indexOf('func (do *delayOffline) offlineFreeRental');
  const fnBlock = hubGoSource.substring(fnIdx, fnIdx + 800);
  assert.ok(fnBlock.includes('not rented, skipping penalty'),
    'should log skip penalty for non-rented machines');
});

test('offlineFreeRental calls NotifyFreeRental with tp=4 (MachineOffline)', () => {
  const fnIdx = hubGoSource.indexOf('func (do *delayOffline) offlineFreeRental');
  const fnBlock = hubGoSource.substring(fnIdx, fnIdx + 2500);
  assert.ok(fnBlock.includes('dbc.DbcChain.NotifyFreeRental(ctx1, 4, info.machine.MachineId)'),
    'should call NotifyFreeRental with tp=4');
});

test('offlineFreeRental has retry loop (max 3)', () => {
  const fnIdx = hubGoSource.indexOf('func (do *delayOffline) offlineFreeRental');
  const fnBlock = hubGoSource.substring(fnIdx, fnIdx + 2500);
  assert.ok(fnBlock.includes('maxRetries = 3'),
    'should define maxRetries = 3');
  assert.ok(fnBlock.includes('retries < maxRetries'),
    'should loop up to maxRetries');
});

test('offlineFreeRental has success tracking variable', () => {
  const fnIdx = hubGoSource.indexOf('func (do *delayOffline) offlineFreeRental');
  const fnBlock = hubGoSource.substring(fnIdx, fnIdx + 2500);
  assert.ok(fnBlock.includes('success := false'),
    'should initialize success = false');
  assert.ok(fnBlock.includes('success = true'),
    'should set success = true on successful notify');
});

test('offlineFreeRental calls SendOnlineNotify on all-retry-failure', () => {
  const fnIdx = hubGoSource.indexOf('func (do *delayOffline) offlineFreeRental');
  const fnBlock = hubGoSource.substring(fnIdx, fnIdx + 2500);
  assert.ok(fnBlock.includes('if !success'),
    'should check success after retry loop');
  // After !success, should still send online notify with empty hash
  const failIdx = fnBlock.indexOf('if !success');
  const failBlock = fnBlock.substring(failIdx, failIdx + 300);
  assert.ok(failBlock.includes('SendOnlineNotify'),
    'should call SendOnlineNotify even on failure');
});

test('IsFreeRentalRented failure falls through to offlineStaked (P0-9 fix)', () => {
  const fnIdx = hubGoSource.indexOf('func (do *delayOffline) offlineFreeRental');
  const fnBlock = hubGoSource.substring(fnIdx, fnIdx + 500);
  assert.ok(fnBlock.includes('falling through to staked path'),
    'should log fallthrough on RPC failure');
  assert.ok(fnBlock.includes('do.offlineStaked(info)'),
    'should call offlineStaked on IsFreeRentalRented failure (P0-9 fix)');
});

test('offlineFreeRental calls OfflineMachine to update DB', () => {
  const fnIdx = hubGoSource.indexOf('func (do *delayOffline) offlineFreeRental');
  const fnBlock = hubGoSource.substring(fnIdx, fnIdx + 1200);
  assert.ok(fnBlock.includes('db.MDB.OfflineMachine'),
    'should call OfflineMachine to persist offline state');
});

// ═══════════════════════════════════════════════════════════════
// SECTION 3: http/contract.go tests
// ═══════════════════════════════════════════════════════════════

console.log('\n=== http/contract.go tests ===\n');

test('contract.go checks FreeRentalEnabled', () => {
  assert.ok(contractGoSource.includes('dbc.DbcChain.FreeRentalEnabled()'),
    'should check FreeRentalEnabled in HTTP handler');
});

test('contract.go checks IsFreeRentalMachine', () => {
  assert.ok(contractGoSource.includes('dbc.DbcChain.IsFreeRentalMachine'),
    'should check IsFreeRentalMachine');
});

test('contract.go checks IsFreeRentalRented with fallthrough on error (P0-10 fix)', () => {
  assert.ok(contractGoSource.includes('dbc.DbcChain.IsFreeRentalRented'),
    'should check IsFreeRentalRented');
  assert.ok(contractGoSource.includes('falling through to staked report'),
    'should fall through to normal report on IsFreeRentalRented error (P0-10 fix)');
});

test('contract.go calls NotifyFreeRental for rented machines', () => {
  assert.ok(contractGoSource.includes('dbc.DbcChain.NotifyFreeRental(ctx1, 4, req.MachineId)'),
    'should call NotifyFreeRental with tp=4');
});

test('contract.go skips penalty for non-rented FreeRental machines', () => {
  assert.ok(contractGoSource.includes('not rented, skipping penalty'),
    'should skip penalty for non-rented free rental machines');
  // Should return OK without calling Report
  const skipIdx = contractGoSource.indexOf('not rented, skipping penalty');
  const afterSkip = contractGoSource.substring(skipIdx, skipIdx + 200);
  assert.ok(afterSkip.includes('StatusOK'),
    'should return OK status when skipping penalty');
});

test('contract.go returns error status on NotifyFreeRental failure', () => {
  // After NotifyFreeRental fails, should return InternalServerError
  const notifyIdx = contractGoSource.indexOf('FreeRental notify offline failed');
  const afterNotify = contractGoSource.substring(notifyIdx, notifyIdx + 200);
  assert.ok(afterNotify.includes('StatusInternalServerError'),
    'should return 500 on notify failure');
});

test('contract.go IsFreeRentalMachine error falls through to normal Report', () => {
  assert.ok(contractGoSource.includes('IsFreeRentalMachine RPC failed'),
    'should log FreeRental check errors');
  assert.ok(contractGoSource.includes('skipping penalty'),
    'should skip penalty on FreeRental check error');
});

test('contract.go calls normal Report for non-FreeRental machines', () => {
  assert.ok(contractGoSource.includes('dbc.DbcChain.Report(ctx1'),
    'should fall through to regular Report for staked machines');
});

// ═══════════════════════════════════════════════════════════════
// SECTION 4: types/config.go tests
// ═══════════════════════════════════════════════════════════════

console.log('\n=== types/config.go tests ===\n');

test('FreeRentalContract field exists in ChainConfig', () => {
  assert.ok(configGoSource.includes('FreeRentalContract'),
    'ChainConfig should have FreeRentalContract field');
});

test('FreeRentalContract uses ContractConfig type', () => {
  assert.ok(configGoSource.includes('FreeRentalContract  ContractConfig'),
    'FreeRentalContract should be of type ContractConfig');
});

test('FreeRentalContract is optional (omitempty)', () => {
  assert.ok(configGoSource.includes('FreeRentalContract,omitempty'),
    'FreeRentalContract should have omitempty json tag');
});

// ═══════════════════════════════════════════════════════════════
// SECTION 5: ABI tests
// ═══════════════════════════════════════════════════════════════

console.log('\n=== free_rental_abi.json tests ===\n');

test('free_rental_abi.json exists and is valid JSON', () => {
  assert.ok(fs.existsSync(abiPath), 'ABI file should exist');
  const content = fs.readFileSync(abiPath, 'utf8');
  const abi = JSON.parse(content); // throws if invalid
  assert.ok(Array.isArray(abi), 'ABI should be a JSON array');
  assert.ok(abi.length > 0, 'ABI should not be empty');
});

test('ABI contains machines function', () => {
  const abi = JSON.parse(fs.readFileSync(abiPath, 'utf8'));
  const machinesFn = abi.find(e => e.name === 'machines' && e.type === 'function');
  assert.ok(machinesFn, 'should have machines function');
  assert.strictEqual(machinesFn.stateMutability, 'view', 'machines should be a view function');
  assert.strictEqual(machinesFn.inputs.length, 1, 'machines should take 1 input (machineId)');
  assert.strictEqual(machinesFn.inputs[0].type, 'string', 'machineId should be string type');
  assert.ok(machinesFn.outputs.length >= 3, 'machines should return at least 3 fields');
  // Verify output structure: (address owner, uint256 pricePerHourUSD, bool registered, bool enabled)
  assert.strictEqual(machinesFn.outputs[0].name, 'owner', 'first output should be owner');
  assert.strictEqual(machinesFn.outputs[2].name, 'registered', 'third output should be registered');
});

test('ABI contains machineIsRented function', () => {
  const abi = JSON.parse(fs.readFileSync(abiPath, 'utf8'));
  const fn = abi.find(e => e.name === 'machineIsRented' && e.type === 'function');
  assert.ok(fn, 'should have machineIsRented function');
  assert.strictEqual(fn.stateMutability, 'view', 'machineIsRented should be a view function');
  assert.strictEqual(fn.inputs.length, 1, 'should take 1 input');
  assert.strictEqual(fn.inputs[0].type, 'string', 'input should be string (machineId)');
  assert.strictEqual(fn.outputs.length, 1, 'should return 1 output');
  assert.strictEqual(fn.outputs[0].type, 'bool', 'output should be bool');
});

test('ABI contains notify function', () => {
  const abi = JSON.parse(fs.readFileSync(abiPath, 'utf8'));
  const fn = abi.find(e => e.name === 'notify' && e.type === 'function');
  assert.ok(fn, 'should have notify function');
  assert.strictEqual(fn.stateMutability, 'nonpayable', 'notify should be nonpayable (state-changing)');
  assert.strictEqual(fn.inputs.length, 2, 'notify should take 2 inputs');
  assert.strictEqual(fn.inputs[0].name, 'tp', 'first input should be tp');
  assert.strictEqual(fn.inputs[0].type, 'uint8', 'tp should be uint8');
  assert.strictEqual(fn.inputs[1].name, 'machineId', 'second input should be machineId');
  assert.strictEqual(fn.inputs[1].type, 'string', 'machineId should be string');
});

test('ABI has exactly 3 functions', () => {
  const abi = JSON.parse(fs.readFileSync(abiPath, 'utf8'));
  const functions = abi.filter(e => e.type === 'function');
  assert.strictEqual(functions.length, 3, `should have exactly 3 functions, got ${functions.length}`);
});

// ═══════════════════════════════════════════════════════════════
// Summary
// ═══════════════════════════════════════════════════════════════

console.log('\n' + '='.repeat(60));
console.log(`DDN Tests: ${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failures.length > 0) {
  console.log('\nFailures:');
  failures.forEach(f => console.log(`  - ${f.name}: ${f.error}`));
}
console.log('='.repeat(60));

process.exit(failed > 0 ? 1 : 0);
