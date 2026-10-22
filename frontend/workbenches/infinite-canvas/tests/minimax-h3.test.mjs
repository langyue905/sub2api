import test from 'node:test';
import assert from 'node:assert/strict';
import { h3Selection, h3VideoOptions, isMiniMaxH3, validateH3References } from '../src/lib/minimax-h3.ts';

// Expected values from the provider contract, independent of the implementation table.
const sizes = {
  '16:9': ['1376x768', '1920x1088'], '9:16': ['768x1376', '1088x1920'],
  '1:1': ['1024x1024', '1440x1440'], '2:3': ['832x1248', '1184x1760'],
  '3:2': ['1248x832', '1760x1184'], '3:4': ['896x1184', '1248x1664'],
  '4:3': ['1184x896', '1664x1248'], '21:9': ['1568x672', '2208x960'],
};
for (const [ratio, fixed] of Object.entries(sizes)) {
  for (const [index, tier] of ['768', '1080', '2k', '4k'].entries()) {
    test(`${tier}/${ratio} preserves the provider size and billing tier`, () => {
      const expected = {
        seconds: 5, resolution: index < 2 ? `${tier}p` : tier,
        size: index < 2 ? fixed[index] : tier.toUpperCase(),
        ...(index >= 2 ? { aspect_ratio: ratio } : {}),
      };
      assert.deepEqual(h3VideoOptions(tier, ratio, '5', 'camera move'), expected);
      assert.equal(h3Selection(tier, ratio).valid, true);
      if (index < 2) assert.deepEqual(h3VideoOptions(tier, fixed[index], '5', 'camera move'), expected);
    });
  }
}
test('invalid persisted settings require reselection and never downgrade', () => {
  for (const [quality, size] of [['480', '16:9'], ['720', 'auto'], ['2160p', '16:9'], ['4320p', '16:9'], ['2k', '1440x1440'], ['768', '1440x1440'], ['1080', 'auto']]) {
    assert.equal(h3Selection(quality, size).valid, false);
    assert.throws(() => h3VideoOptions(quality, size, '5', 'test'));
  }
  assert.equal(h3VideoOptions('1080', '1440x1440', '5', 'test').resolution, '1080p');
});
test('duration and prompt are checked before a paid submission', () => {
  for (const value of ['4', '15']) assert.equal(h3VideoOptions('768', '16:9', value, 'test').seconds, Number(value));
  for (const value of ['', '3', '16', '4.5', 'NaN', 'Infinity']) assert.throws(() => h3VideoOptions('768', '16:9', value, 'test'));
  assert.throws(() => h3VideoOptions('768', '16:9', '5', '   '));
});
test('reference limits and image prerequisite', () => {
  validateH3References(0, 0, 0, '768');
  validateH3References(9, 3, 3, '1080');
  for (const counts of [[10, 0, 0], [1, 4, 0], [1, 0, 4], [0, 1, 0], [0, 0, 1]]) assert.throws(() => validateH3References(...counts, '768'));
});
test('model identity supports channel-qualified names without matching legacy variants', () => {
  assert.equal(isMiniMaxH3('channel::MiniMax-H3'), true);
  for (const model of ['grok-video-1.5', 'Minimax-H3-768p-933-10s', '']) assert.equal(isMiniMaxH3(model), false);
});

test('2K/4K super-resolution requires an image before submission', () => {
  for (const tier of ['2k', '4k']) {
    for (const counts of [[0, 0, 0], [0, 1, 0], [0, 0, 1]]) {
      assert.throws(() => validateH3References(...counts, tier), /超分至少需要一张参考图/);
    }
    validateH3References(1, 0, 0, tier);
    validateH3References(9, 3, 3, tier);
  }
});
