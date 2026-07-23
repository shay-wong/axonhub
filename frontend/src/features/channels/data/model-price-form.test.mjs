import assert from 'node:assert/strict';
import test from 'node:test';
import {
  collectPriceFormValidationIssues,
  mapPriceFormDataToSaveInput,
  mapServerPricesToFormData,
  replaceCatalogServiceTierPrices,
} from './model-price-form.ts';

const usageItem = (itemCode, usagePerUnit) => ({
  itemCode,
  pricing: {
    mode: 'usage_per_unit',
    usagePerUnit,
  },
});

const tieredItem = (itemCode, mode, bounds) => ({
  itemCode,
  pricing: {
    mode,
    usageTiered: {
      tiers: [...bounds.map((upTo) => ({ upTo, pricePerUnit: '1' })), { upTo: null, pricePerUnit: '2' }],
    },
  },
});

test('preserves service-tier prices through query, form, and mutation mapping', () => {
  const serverPrices = [
    {
      id: 'price_1',
      modelID: 'gpt-5.3-codex',
      price: {
        items: [
          {
            ...usageItem('prompt_tokens', 1.25),
            promptWriteCacheVariants: [
              {
                variantCode: 'five_min',
                pricing: {
                  mode: 'usage_tiered',
                  usageTiered: {
                    tiers: [
                      { upTo: 1000, pricePerUnit: 0.5 },
                      { upTo: null, pricePerUnit: 0.25 },
                    ],
                  },
                },
              },
            ],
          },
        ],
        serviceTierPrices: [
          {
            serviceTier: ' PRIORITY ',
            items: [
              usageItem('prompt_tokens', 2.5),
              {
                itemCode: 'completion_tokens',
                pricing: {
                  mode: 'flat_fee',
                  flatFee: 3,
                },
              },
            ],
          },
        ],
      },
    },
  ];

  const formData = mapServerPricesToFormData(serverPrices);

  assert.equal(formData.prices[0].price.items[0].pricing.usagePerUnit, '1.25');
  assert.equal(formData.prices[0].price.items[0].promptWriteCacheVariants[0].pricing.usageTiered.tiers[0].pricePerUnit, '0.5');
  assert.equal(formData.prices[0].price.serviceTierPrices[0].items[0].pricing.usagePerUnit, '2.5');
  assert.equal(formData.prices[0].price.serviceTierPrices[0].items[1].pricing.flatFee, '3');

  assert.deepEqual(mapPriceFormDataToSaveInput(formData), [
    {
      modelId: 'gpt-5.3-codex',
      price: {
        items: [
          {
            itemCode: 'prompt_tokens',
            pricing: {
              mode: 'usage_per_unit',
              flatFee: null,
              usagePerUnit: '1.25',
              usageTiered: null,
            },
            promptWriteCacheVariants: [
              {
                variantCode: 'five_min',
                pricing: {
                  mode: 'usage_tiered',
                  flatFee: null,
                  usagePerUnit: null,
                  usageTiered: {
                    tiers: [
                      { upTo: 1000, pricePerUnit: '0.5' },
                      { upTo: null, pricePerUnit: '0.25' },
                    ],
                  },
                },
              },
            ],
          },
        ],
        serviceTierPrices: [
          {
            serviceTier: 'priority',
            items: [
              {
                itemCode: 'prompt_tokens',
                pricing: {
                  mode: 'usage_per_unit',
                  flatFee: null,
                  usagePerUnit: '2.5',
                  usageTiered: null,
                },
                promptWriteCacheVariants: [],
              },
              {
                itemCode: 'completion_tokens',
                pricing: {
                  mode: 'flat_fee',
                  flatFee: '3',
                  usagePerUnit: null,
                  usageTiered: null,
                },
                promptWriteCacheVariants: [],
              },
            ],
          },
        ],
        schedule: null,
      },
    },
  ]);
});

test('preserves scheduled prompt cache variants through form and mutation mapping', () => {
  // Scheduled items use the same billing shape as base items and must not lose variant prices on save.
  const serverPrices = [
    {
      id: 'price_1',
      modelID: 'gpt-5.3-codex',
      price: {
        items: [usageItem('prompt_tokens', 1)],
        serviceTierPrices: [],
        schedule: {
          timezone: 'UTC',
          overrides: [
            {
              name: 'Night cache price',
              priority: 1,
              when: { weekdays: [1, 2, 3, 4, 5] },
              items: [
                {
                  ...usageItem('prompt_write_cached_tokens', 4),
                  promptWriteCacheVariants: [
                    {
                      variantCode: 'one_hour',
                      pricing: { mode: 'flat_fee', flatFee: 1.5 },
                    },
                  ],
                },
              ],
            },
          ],
        },
      },
    },
  ];

  const formData = mapServerPricesToFormData(serverPrices);
  assert.equal(formData.prices[0].price.schedule.overrides[0].items[0].pricing.usagePerUnit, '4');
  assert.equal(formData.prices[0].price.schedule.overrides[0].items[0].promptWriteCacheVariants[0].pricing.flatFee, '1.5');

  const savedSchedule = mapPriceFormDataToSaveInput(formData)[0].price.schedule;
  assert.deepEqual(savedSchedule, {
    timezone: 'UTC',
    overrides: [
      {
        name: 'Night cache price',
        priority: 1,
        when: {
          dailyTime: null,
          weekdays: [1, 2, 3, 4, 5],
          dateRange: null,
        },
        items: [
          {
            itemCode: 'prompt_write_cached_tokens',
            pricing: {
              mode: 'usage_per_unit',
              flatFee: null,
              usagePerUnit: '4',
              usageTiered: null,
            },
            promptWriteCacheVariants: [
              {
                variantCode: 'one_hour',
                pricing: {
                  mode: 'flat_fee',
                  flatFee: '1.5',
                  usagePerUnit: null,
                  usageTiered: null,
                },
              },
            ],
          },
        ],
      },
    ],
  });
});

test('catalog application replaces stale service-tier prices without replacing manual base prices', () => {
  const currentPrice = {
    items: [usageItem('prompt_tokens', '9')],
    serviceTierPrices: [
      {
        serviceTier: 'stale',
        items: [usageItem('prompt_tokens', '99')],
      },
    ],
  };
  const catalogPrice = {
    items: [usageItem('prompt_tokens', '1')],
    serviceTierPrices: [
      {
        serviceTier: 'priority',
        items: [usageItem('prompt_tokens', '2')],
      },
    ],
  };

  const result = replaceCatalogServiceTierPrices(currentPrice, catalogPrice);

  assert.deepEqual(result.items, currentPrice.items);
  assert.deepEqual(result.serviceTierPrices, catalogPrice.serviceTierPrices);
  assert.notEqual(result.serviceTierPrices, catalogPrice.serviceTierPrices);
  assert.deepEqual(currentPrice.serviceTierPrices[0].serviceTier, 'stale');
});

test('validates service-tier names and nested price items with the same rules as base prices', () => {
  const formData = {
    prices: [
      {
        modelId: 'gpt-5.3-codex',
        price: {
          items: [usageItem('prompt_tokens', '1')],
          serviceTierPrices: [
            {
              serviceTier: ' ',
              items: [usageItem('prompt_tokens', '')],
            },
            {
              serviceTier: 'priority',
              items: [usageItem('prompt_tokens', '2'), usageItem('prompt_tokens', '3')],
            },
            {
              serviceTier: ' Priority ',
              items: [
                {
                  itemCode: 'prompt_write_cached_tokens',
                  pricing: { mode: 'usage_per_unit', usagePerUnit: '4' },
                  promptWriteCacheVariants: [
                    {
                      variantCode: 'five_min',
                      pricing: { mode: 'flat_fee', flatFee: '' },
                    },
                    {
                      variantCode: 'five_min',
                      pricing: { mode: 'flat_fee', flatFee: '1' },
                    },
                  ],
                },
              ],
            },
          ],
        },
      },
    ],
  };

  const issues = collectPriceFormValidationIssues(formData);
  const issueCodes = issues.map((issue) => issue.code);

  assert.ok(issueCodes.includes('serviceTierRequired'));
  assert.equal(issueCodes.filter((code) => code === 'duplicateServiceTier').length, 2);
  assert.ok(issueCodes.includes('priceRequired'));
  assert.equal(issueCodes.filter((code) => code === 'duplicateItemCode').length, 2);
  assert.equal(issueCodes.filter((code) => code === 'duplicateVariantCode').length, 2);
});

test('validates scheduled items with the same duplicate rules as base prices', () => {
  // Schedule overrides become the active billing list, so duplicate items and variants must be rejected before save.
  const formData = {
    prices: [
      {
        modelId: 'gpt-5.3-codex',
        price: {
          items: [usageItem('prompt_tokens', '1')],
          serviceTierPrices: [],
          schedule: {
            timezone: 'UTC',
            overrides: [
              {
                name: 'Duplicate schedule prices',
                priority: 1,
                when: { weekdays: [1] },
                items: [
                  usageItem('prompt_tokens', '2'),
                  {
                    itemCode: 'prompt_tokens',
                    pricing: { mode: 'usage_per_unit', usagePerUnit: '3' },
                    promptWriteCacheVariants: [
                      { variantCode: 'five_min', pricing: { mode: 'flat_fee', flatFee: '1' } },
                      { variantCode: 'five_min', pricing: { mode: 'flat_fee', flatFee: '2' } },
                    ],
                  },
                ],
              },
            ],
          },
        },
      },
    ],
  };

  const issues = collectPriceFormValidationIssues(formData);
  assert.equal(issues.filter((issue) => issue.code === 'duplicateItemCode').length, 2);
  assert.equal(issues.filter((issue) => issue.code === 'duplicateVariantCode').length, 2);
  assert.ok(issues.every((issue) => issue.path.includes('schedule')));
});

test('accepts positive strictly increasing tier bounds for usage tiered and usage volume prices', () => {
  const formData = {
    prices: [
      {
        modelId: 'gpt-5.3-codex',
        price: {
          items: [tieredItem('prompt_tokens', 'usage_tiered', [100, 200]), tieredItem('completion_tokens', 'usage_volume', [100, 200])],
          serviceTierPrices: [],
        },
      },
    ],
  };

  assert.deepEqual(collectPriceFormValidationIssues(formData), []);
});

test('rejects non-positive and non-increasing tier bounds in base and service-tier prices', () => {
  const cases = [
    {
      name: 'usage tiered decreasing bounds',
      item: tieredItem('prompt_tokens', 'usage_tiered', [200, 100]),
      serviceTierPath: false,
      invalidTierIndex: 1,
    },
    {
      name: 'usage volume duplicate bounds',
      item: tieredItem('prompt_tokens', 'usage_volume', [100, 100]),
      serviceTierPath: false,
      invalidTierIndex: 1,
    },
    {
      name: 'service tier usage tiered zero bound',
      item: tieredItem('prompt_tokens', 'usage_tiered', [0]),
      serviceTierPath: true,
      invalidTierIndex: 0,
    },
    {
      name: 'service tier usage volume negative bound',
      item: tieredItem('prompt_tokens', 'usage_volume', [-1]),
      serviceTierPath: true,
      invalidTierIndex: 0,
    },
  ];

  cases.forEach(({ name, item, serviceTierPath, invalidTierIndex }) => {
    const formData = {
      prices: [
        {
          modelId: 'gpt-5.3-codex',
          price: {
            items: serviceTierPath ? [usageItem('prompt_tokens', '1')] : [item],
            serviceTierPrices: serviceTierPath
              ? [
                  {
                    serviceTier: 'priority',
                    items: [item],
                  },
                ]
              : [],
          },
        },
      ],
    };
    const itemPath = serviceTierPath ? ['prices', 0, 'price', 'serviceTierPrices', 0, 'items', 0] : ['prices', 0, 'price', 'items', 0];

    assert.deepEqual(
      collectPriceFormValidationIssues(formData),
      [
        {
          code: 'priceRequired',
          path: [...itemPath, 'pricing', 'usageTiered', 'tiers', invalidTierIndex, 'upTo'],
        },
      ],
      name
    );
  });
});
