import { useState, useEffect, useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ui, useCopyToClipboard } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import {
  useConfigureProvider,
  useProvider,
  providerKeys,
} from '@/hooks/vault/use-providers'
import type { ConfigureProviderParams } from '@/server/providers'
import { toast } from '@everstack/ui/components'
import { cn } from '@everstack/utils/functions/cn'
import { ModelDiscoveryDialog } from './model-discovery-dialog'
import { useCustomModels } from '@/hooks/use-model-discovery'
import { APIKeyRow } from './api-key-row'
import { ProviderDisplay } from './provider-icon'
import {
  useProviderAPIKeys,
  useAddProviderAPIKey,
  useUpdateAPIKeyWeight,
  useToggleAPIKey,
  useDeleteProviderAPIKey,
} from '@/hooks/vault/use-provider-api-keys'
import type { ModelMetadata } from '@everstack/proto/everstack/providers/providers_pb'
import type { CustomModel } from '@everstack/proto/everstack/providers/model_discovery_pb'
import type { ModelParameter } from '@everstack/proto/everstack/providers/providers_pb'
import { ParameterControl } from './parameter-control'

const {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
  SheetFooter,
  Button,
  Input,
  Label,
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
  InputWithIcon,
  Checkbox,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} = ui

interface ConfigureProviderSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  providerName: string | null
}

type ModelParameterValues = Record<string, Record<string, string>>

const MODEL_PARAMETERS_SETTING = 'model_parameters'
const REASONING_PARAMETER_KEYS = [
  'reasoning_effort',
  'reasoning_budget_tokens',
  'reasoning_enabled',
] as const

function isReasoningParameterKey(key: string) {
  return (REASONING_PARAMETER_KEYS as readonly string[]).includes(key)
}
// The request parameters a provider-wide default may set, mirroring the
// backend's providerParameterKeys. Which of these a given provider actually
// shows is decided by its catalog; this list only says which custom_settings
// keys belong to the parameter tier at all, so that reading and writing them
// stay symmetric and unrelated settings are left alone.
function parseModelParameterValues(value?: string): ModelParameterValues {
  if (!value) return {}
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object'
      ? (parsed as ModelParameterValues)
      : {}
  } catch {
    return {}
  }
}

// The gateway seeds a placeholder "config" model for providers configured
// entirely from YAML. It is not a real model and must not reach the picker.
function isSyntheticModelName(name: string) {
  return name.trim().toLowerCase() === 'config'
}

const PROVIDER_PARAMETER_KEYS = [
  'max_output_tokens',
  'temperature',
  'top_p',
  'top_k',
  'frequency_penalty',
  'presence_penalty',
  'seed',
  'verbosity',
  'reasoning_effort',
  'reasoning_budget_tokens',
  'reasoning_enabled',
] as const

// `max_tokens` is what this tier called the output cap before the model tier
// named the same control `max_output_tokens`. Read either, write the latter.
const LEGACY_MAX_TOKENS_KEY = 'max_tokens'

function readProviderParameters(
  customSettings: Record<string, string> | undefined,
): Record<string, string> {
  const values: Record<string, string> = {}
  for (const key of PROVIDER_PARAMETER_KEYS) {
    const value = customSettings?.[key]
    if (value !== undefined && value !== '') values[key] = value
  }
  const legacyMaxTokens = customSettings?.[LEGACY_MAX_TOKENS_KEY]
  if (legacyMaxTokens && !values.max_output_tokens) {
    values.max_output_tokens = legacyMaxTokens
  }
  return values
}

/**
 * Every control any enabled model accepts, deduplicated, with the widest
 * bounds offered for each. A provider-wide value only reaches the models that
 * declare that control - the gateway drops it for the rest - so showing the
 * union costs nothing and setting one control does not require every model to
 * agree on it.
 */
function providerTierParameters(
  models: { name: string; parameters: ModelParameter[] }[],
): ModelParameter[] {
  const byKey = new Map<string, ModelParameter>()
  for (const model of models) {
    for (const parameter of model.parameters) {
      const seen = byKey.get(parameter.key)
      if (!seen) {
        byKey.set(parameter.key, parameter)
        continue
      }
      byKey.set(parameter.key, {
        ...seen,
        hasMinValue: seen.hasMinValue && parameter.hasMinValue,
        minValue: Math.min(seen.minValue, parameter.minValue),
        hasMaxValue: seen.hasMaxValue && parameter.hasMaxValue,
        maxValue: Math.max(seen.maxValue, parameter.maxValue),
        options: Array.from(new Set([...seen.options, ...parameter.options])),
      })
    }
  }
  return Array.from(byKey.values())
}

export function ConfigureProviderSheet({
  open,
  onOpenChange,
  providerName,
}: ConfigureProviderSheetProps) {
  const queryClient = useQueryClient()
  const [activeTab, setActiveTab] = useState('general')

  // General Settings
  const [customBaseUrl, setCustomBaseUrl] = useState('')
  const [isDefaultProvider, setIsDefaultProvider] = useState<boolean>(false)
  const [defaultModel, setDefaultModel] = useState<string>('')
  const [selectedModels, setSelectedModels] = useState<Set<string>>(new Set())

  // Model-scoped request defaults
  const [parameterModel, setParameterModel] = useState('')
  const [modelParameterValues, setModelParameterValues] =
    useState<ModelParameterValues>({})
  // Provider-wide defaults: one set of values applied to every model under
  // this provider unless that model overrides the same control.
  const [providerParameterValues, setProviderParameterValues] = useState<
    Record<string, string>
  >({})

  // UI State
  const [discoveryDialogOpen, setDiscoveryDialogOpen] = useState(false)
  const [showAddKeyForm, setShowAddKeyForm] = useState(false)
  const [newKeyName, setNewKeyName] = useState('')
  const [newKeyValue, setNewKeyValue] = useState('')
  const [newKeyWeight, setNewKeyWeight] = useState(1)
  const [modelSearchQuery, setModelSearchQuery] = useState('')
  const [copy] = useCopyToClipboard()

  const { data: providerData, isLoading: isLoadingProvider } = useProvider(
    providerName ?? undefined,
  )
  const configureProviderMutation = useConfigureProvider()
  const { data: customModelsData } = useCustomModels(providerName || undefined)

  const providerStatus = providerData?.provider
  const catalog = providerStatus?.catalog
  const configuration = providerStatus?.configuration
  const providerConfigId = configuration?.id

  // API Key Management Hooks
  const { data: apiKeysData } = useProviderAPIKeys(providerConfigId || '')
  const addKeyMutation = useAddProviderAPIKey()
  const updateWeightMutation = useUpdateAPIKeyWeight()
  const toggleKeyMutation = useToggleAPIKey()
  const deleteKeyMutation = useDeleteProviderAPIKey()

  const apiKeys = apiKeysData?.keys || []

  // Check if this is a meta-provider that supports model discovery
  const isMetaProvider = catalog?.providerType === 'meta'
  const supportsDiscovery = catalog?.supportsModelDiscovery === true

  // 1. Deduplicate Catalog Models
  const uniqueCatalogModelsMap = new Map<string, ModelMetadata>()
  ;(catalog?.models || []).forEach((m) => {
    if (isSyntheticModelName(m.name)) return
    if (m.status === 'deprecated' && !selectedModels.has(m.name)) return
    uniqueCatalogModelsMap.set(m.name, m)
  })
  const uniqueCatalogModelsList = Array.from(uniqueCatalogModelsMap.values())

  // 2. Build Set of keys that will be rendered
  const renderedCatalogKeys = new Set(
    uniqueCatalogModelsList.map((m) => m.name),
  )

  // 3. Deduplicate Custom Models and filter out collisions
  const uniqueCustomModelsMap = new Map<string, CustomModel>()
  ;(customModelsData?.models || []).forEach((m) => {
    if (isSyntheticModelName(m.modelName)) return
    // Only add if not in catalog AND not already in custom map
    if (!renderedCatalogKeys.has(m.modelName)) {
      uniqueCustomModelsMap.set(m.modelName, m)
    }
  })
  const uniqueCustomModelsList = Array.from(uniqueCustomModelsMap.values())

  // 4. Filter models based on search query
  const searchLower = modelSearchQuery.toLowerCase().trim()
  const filteredCatalogModels = searchLower
    ? uniqueCatalogModelsList.filter(
        (m) =>
          m.name.toLowerCase().includes(searchLower) ||
          m.displayName.toLowerCase().includes(searchLower),
      )
    : uniqueCatalogModelsList

  const filteredCustomModels = searchLower
    ? uniqueCustomModelsList.filter(
        (m) =>
          m.modelName.toLowerCase().includes(searchLower) ||
          m.displayName.toLowerCase().includes(searchLower),
      )
    : uniqueCustomModelsList

  // Reset form when provider changes or dialog opens
  useEffect(() => {
    if (open && providerStatus) {
      // General
      setCustomBaseUrl(configuration?.customBaseUrl || catalog?.baseUrl || '')
      setIsDefaultProvider(configuration?.customSettings?.default === 'true')
      const enabledModels = (configuration?.enabledModels || []).filter(
        (modelName) => !isSyntheticModelName(modelName),
      )
      const configuredDefaultModel =
        configuration?.customSettings?.default_alias || ''
      const validDefaultModel = enabledModels.includes(configuredDefaultModel)
        ? configuredDefaultModel
        : enabledModels[0] || ''
      setDefaultModel(validDefaultModel)
      setSelectedModels(new Set(enabledModels))
      setParameterModel(validDefaultModel)
      setModelParameterValues(
        parseModelParameterValues(
          configuration?.customSettings?.[MODEL_PARAMETERS_SETTING],
        ),
      )
      setProviderParameterValues(
        readProviderParameters(configuration?.customSettings),
      )

      // UI
      setShowAddKeyForm(false)
      setNewKeyName('')
      setNewKeyValue('')
      setNewKeyWeight(1)
      setActiveTab('general')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, providerName, providerConfigId])

  // Copy Handler
  const handleCopy = useCallback(
    (modelName: string) => {
      copy(modelName)
      toast.success(`Copied "${modelName}" to clipboard`)
    },
    [copy],
  )

  // API Key Management Handlers
  const handleAddKey = async () => {
    if (!newKeyName.trim() || !newKeyValue.trim()) {
      toast.error('Please fill in all API key fields')
      return
    }

    try {
      let configId = providerConfigId
      const isInitialSetup = !configId

      // If provider is not configured yet, configure it first with the API key
      if (isInitialSetup) {
        const modelsToUse =
          selectedModels.size > 0
            ? Array.from(selectedModels)
            : uniqueCatalogModelsList[0]?.name
              ? [uniqueCatalogModelsList[0].name]
              : []

        const customSettings: Record<string, string> = {}
        if (isDefaultProvider) customSettings.default = 'true'
        if (defaultModel) customSettings.default_alias = defaultModel
        if (Object.keys(modelParameterValues).length > 0) {
          customSettings[MODEL_PARAMETERS_SETTING] =
            JSON.stringify(modelParameterValues)
        }

        const configureResult = await configureProviderMutation.mutateAsync({
          providerName: providerName!,
          apiKey: newKeyValue.trim(),
          apiKeyName: newKeyName.trim(),
          apiKeyWeight: newKeyWeight,
          enabledModels: modelsToUse,
          customBaseUrl: customBaseUrl.trim() || catalog?.baseUrl || undefined,
          customSettings,
        })

        configId = configureResult?.provider?.configuration?.id

        if (!configId) {
          throw new Error('Failed to get provider configuration ID after setup')
        }
      }

      if (!configId) throw new Error('Provider configuration ID is missing')

      if (!isInitialSetup) {
        await addKeyMutation.mutateAsync({
          providerConfigId: configId,
          keyName: newKeyName.trim(),
          apiKey: newKeyValue.trim(),
          weight: newKeyWeight,
        })
      }

      await queryClient.invalidateQueries({ queryKey: providerKeys.all })

      toast.success(`API key "${newKeyName}" added successfully`)
      setShowAddKeyForm(false)
      setNewKeyName('')
      setNewKeyValue('')
      setNewKeyWeight(1)
    } catch (error) {
      console.error('[AddKey] Error:', error)
      toast.error(
        `Failed to add API key: ${error instanceof Error ? error.message : 'Unknown error'}`,
      )
    }
  }

  const handleUpdateWeight = async (keyId: string, weight: number) => {
    try {
      await updateWeightMutation.mutateAsync({ keyId, weight })
      toast.success('API key weight updated')
    } catch (error) {
      toast.error(
        `Failed to update weight: ${error instanceof Error ? error.message : 'Unknown error'}`,
      )
    }
  }

  const handleToggleKey = async (keyId: string, isActive: boolean) => {
    try {
      await toggleKeyMutation.mutateAsync({ keyId, isActive })
      toast.success(`API key ${isActive ? 'activated' : 'deactivated'}`)
    } catch (error) {
      toast.error(
        `Failed to toggle API key: ${error instanceof Error ? error.message : 'Unknown error'}`,
      )
    }
  }

  const handleDeleteKey = async (keyId: string) => {
    if (!confirm('Are you sure you want to delete this API key?')) return

    try {
      await deleteKeyMutation.mutateAsync({ keyId })
      toast.success('API key deleted successfully')
    } catch (error) {
      toast.error(
        `Failed to delete API key: ${error instanceof Error ? error.message : 'Unknown error'}`,
      )
    }
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()

    if (!providerName) return

    if (!providerConfigId) {
      toast.error('Please add at least one API key to configure this provider')
      return
    }

    if (selectedModels.size === 0) {
      toast.error('Please select at least one model')
      return
    }

    if (!customBaseUrl.trim()) {
      toast.error('Base URL is required')
      return
    }

    // Rebuild custom_settings from what the sheet owns: the provider-wide
    // parameter tier, the model tier, and the default-provider flags. Anything
    // else a configuration carries is preserved untouched.
    const customSettings: Record<string, string> = {
      ...(configuration?.customSettings || {}),
    }
    delete customSettings.default
    delete customSettings.default_alias
    delete customSettings[MODEL_PARAMETERS_SETTING]
    delete customSettings[LEGACY_MAX_TOKENS_KEY]
    for (const key of PROVIDER_PARAMETER_KEYS) delete customSettings[key]
    for (const [key, value] of Object.entries(providerParameterValues)) {
      if (value !== '') customSettings[key] = value
    }

    if (isDefaultProvider) customSettings.default = 'true'
    if (defaultModel) customSettings.default_alias = defaultModel
    const configuredModelParameters = Object.fromEntries(
      Object.entries(modelParameterValues)
        .filter(([modelName]) => selectedModels.has(modelName))
        .map(([modelName, values]) => [
          modelName,
          Object.fromEntries(
            Object.entries(values).filter(([, value]) => value !== ''),
          ),
        ])
        .filter(([, values]) => Object.keys(values).length > 0),
    )
    if (Object.keys(configuredModelParameters).length > 0) {
      customSettings[MODEL_PARAMETERS_SETTING] = JSON.stringify(
        configuredModelParameters,
      )
    }

    const params: ConfigureProviderParams = {
      providerName,
      apiKey: undefined, // API keys are managed separately
      enabledModels: Array.from(selectedModels),
      customBaseUrl: customBaseUrl.trim() || undefined,
      customSettings,
    }

    try {
      await configureProviderMutation.mutateAsync(params)
      // Also refresh gateway models so the agent form model selector picks up new models
      await queryClient.invalidateQueries({ queryKey: ['gateway-models'] })
      toast.success(
        `${catalog?.displayName || providerName} settings updated successfully`,
      )
      onOpenChange(false)
    } catch (error) {
      toast.error(
        `Failed to update provider: ${error instanceof Error ? error.message : 'Unknown error'}`,
      )
    }
  }

  const toggleModel = (modelName: string) => {
    const newSet = new Set(selectedModels)
    if (newSet.has(modelName)) {
      newSet.delete(modelName)
      if (defaultModel === modelName) setDefaultModel('')
      if (parameterModel === modelName) {
        setParameterModel(Array.from(newSet)[0] || '')
      }
    } else {
      newSet.add(modelName)
      if (newSet.size === 1) setDefaultModel(modelName)
      if (!parameterModel) setParameterModel(modelName)
    }
    setSelectedModels(newSet)
  }

  const selectAllModels = () => {
    if (uniqueCatalogModelsList.length > 0) {
      const allModels = new Set(
        uniqueCatalogModelsList.map((model) => model.name),
      )
      const firstModel = uniqueCatalogModelsList[0].name
      setSelectedModels(allModels)
      if (!defaultModel && allModels.size > 0) {
        setDefaultModel(firstModel)
      }
      if (!parameterModel) {
        setParameterModel(firstModel)
      }
    }
  }

  const deselectAllModels = () => {
    setSelectedModels(new Set())
    setDefaultModel('')
    setParameterModel('')
  }

  const resetParameterDefaults = () => {
    if (!parameterModel) return
    setModelParameterValues((current) => {
      const next = { ...current }
      delete next[parameterModel]
      return next
    })
  }

  const setModelParameter = (
    modelName: string,
    key: string,
    value?: string,
  ) => {
    setModelParameterValues((current) => {
      const values = { ...(current[modelName] || {}) }
      if (value && isReasoningParameterKey(key)) {
        const conflicts =
          providerName === 'anthropic'
            ? key === 'reasoning_budget_tokens'
              ? ['reasoning_enabled']
              : key === 'reasoning_enabled'
                ? ['reasoning_budget_tokens']
                : []
            : REASONING_PARAMETER_KEYS.filter(
                (reasoningKey) => reasoningKey !== key,
              )
        for (const conflict of conflicts) delete values[conflict]
      }
      if (value === undefined || value === '') delete values[key]
      else values[key] = value

      const next = { ...current }
      if (Object.keys(values).length === 0) delete next[modelName]
      else next[modelName] = values
      return next
    })
  }

  const selectedParameterModel = uniqueCatalogModelsList.find(
    (model) => model.name === parameterModel,
  )

  // Which provider-wide controls exist is a property of the provider the sheet
  // is open on, via the models enabled under it.
  const providerParameters = providerTierParameters(
    uniqueCatalogModelsList.filter((model) => selectedModels.has(model.name)),
  )

  const setProviderParameter = (key: string, value?: string) => {
    setProviderParameterValues((current) => {
      const next = { ...current }
      if (value === undefined || value === '') delete next[key]
      else next[key] = value
      return next
    })
  }

  const resetProviderDefaults = () => setProviderParameterValues({})

  const selectedVariantFor = (
    model: (typeof uniqueCatalogModelsList)[number],
  ) =>
    model.variants.find((variant) =>
      Object.entries(variant.parameters).every(
        ([key, value]) => modelParameterValues[model.name]?.[key] === value,
      ),
    )?.id || '__default__'

  const setModelVariant = (
    model: (typeof uniqueCatalogModelsList)[number],
    variantID: string,
  ) => {
    const variantKeys = new Set(
      model.variants.flatMap((variant) => Object.keys(variant.parameters)),
    )
    setModelParameterValues((current) => {
      const values = { ...(current[model.name] || {}) }
      for (const key of variantKeys) delete values[key]
      const variant = model.variants.find((item) => item.id === variantID)
      if (variant) {
        const selectsReasoning = Object.keys(variant.parameters).some((key) =>
          isReasoningParameterKey(key),
        )
        if (selectsReasoning && providerName !== 'anthropic') {
          for (const key of REASONING_PARAMETER_KEYS) delete values[key]
        }
        Object.assign(values, variant.parameters)
      }

      const next = { ...current }
      if (Object.keys(values).length === 0) delete next[model.name]
      else next[model.name] = values
      return next
    })
  }

  return (
    <>
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent className="bg-brand-main-900 border-l-brand-main-500 w-full sm:max-w-2xl  scrollbar-macos flex flex-col">
          <SheetHeader className="flex items-center space-x-2.5">
            <SheetTitle className="text-white light:text-brand-main-50 text-base font-semibold flex items-center gap-2">
              <ProviderDisplay
                providerName={providerName || ''}
                isActive={true}
              />
              <span>Configure {catalog?.displayName || providerName}</span>
            </SheetTitle>
            <SheetDescription className="text-white/60 light:text-black/60 mt-1 text-xs">
              Manage credentials, routing, models, and model-specific defaults.
            </SheetDescription>
          </SheetHeader>

          {isLoadingProvider ? (
            <div className="flex-1 flex items-center justify-center">
              <div className="text-white/60 light:text-black/60">
                Loading provider details...
              </div>
            </div>
          ) : (
            <form
              onSubmit={handleSubmit}
              className="flex-1 flex flex-col min-h-0"
            >
              <Tabs
                value={activeTab}
                onValueChange={setActiveTab}
                className="flex-1 flex flex-col min-h-0"
              >
                <TabsList className="mt-2 ml-4 w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                  <TabsTrigger
                    value="general"
                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1 px-3"
                  >
                    General
                  </TabsTrigger>
                  <TabsTrigger
                    value="parameters"
                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1 px-3"
                  >
                    Parameters
                  </TabsTrigger>
                  <TabsTrigger
                    value="models"
                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1 px-3"
                  >
                    Models
                  </TabsTrigger>
                </TabsList>

                <div className="flex-1 min-h-0 overflow-hidden pb-10 px-4">
                  <TabsContent
                    value="general"
                    className="space-y-6 mt-0 h-full overflow-y-auto scrollbar-macos"
                  >
                    {/* API Keys Section */}
                    <div className="space-y-2">
                      <div className="flex items-center justify-between">
                        <Label className="text-white light:text-brand-main-50 font-medium">
                          API Keys
                        </Label>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() => setShowAddKeyForm(!showAddKeyForm)}
                          className="text-blue-400 light:text-blue-600 hover:text-blue-300 light:hover:text-blue-600 hover:bg-blue-500/10 h-8"
                        >
                          <Iconify.Icon
                            icon={
                              showAddKeyForm
                                ? 'heroicons:x-mark'
                                : 'heroicons:plus'
                            }
                            className="size-4 mr-1"
                          />
                          {showAddKeyForm ? 'Cancel' : 'Add API Key'}
                        </Button>
                      </div>

                      {showAddKeyForm && (
                        <div className="border border-brand-main-500 rounded p-4 space-y-3 bg-brand-main-800/50 animate-in slide-in-from-top-2 duration-200">
                          <div className="space-y-2">
                            <Label
                              htmlFor="newKeyName"
                              className="text-white light:text-brand-main-50 text-xs"
                            >
                              Key Name
                            </Label>
                            <Input
                              id="newKeyName"
                              value={newKeyName}
                              onChange={(e) => setNewKeyName(e.target.value)}
                              placeholder="e.g., Production Key"
                              className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm"
                              required
                            />
                          </div>
                          <div className="space-y-2">
                            <Label
                              htmlFor="newKeyValue"
                              className="text-white light:text-brand-main-50 text-xs"
                            >
                              API Key
                            </Label>
                            <Input
                              id="newKeyValue"
                              type="password"
                              value={newKeyValue}
                              onChange={(e) => setNewKeyValue(e.target.value)}
                              placeholder="sk-..."
                              className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm"
                              required
                            />
                          </div>
                          <div className="space-y-2">
                            <Label
                              htmlFor="newKeyWeight"
                              className="text-white light:text-brand-main-50 text-xs"
                            >
                              Weight
                            </Label>
                            <div className="flex items-center gap-3">
                              <Input
                                id="newKeyWeight"
                                type="number"
                                min="1"
                                value={newKeyWeight}
                                onChange={(e) =>
                                  setNewKeyWeight(parseInt(e.target.value) || 1)
                                }
                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm w-20"
                              />
                              <span className="text-xs text-white/65 light:text-black/65">
                                Higher weight = more traffic
                              </span>
                            </div>
                          </div>
                          <Button
                            type="button"
                            onClick={handleAddKey}
                            size="sm"
                            disabled={addKeyMutation.isPending}
                            className="w-full mt-2"
                          >
                            {addKeyMutation.isPending
                              ? 'Adding...'
                              : 'Add API Key'}
                          </Button>
                        </div>
                      )}

                      <div className="space-y-2">
                        {apiKeys.length > 0 ? (
                          apiKeys.map((key) => (
                            <APIKeyRow
                              key={key.id}
                              apiKey={key}
                              onUpdateWeight={handleUpdateWeight}
                              onToggle={handleToggleKey}
                              onDelete={handleDeleteKey}
                            />
                          ))
                        ) : (
                          <div className="text-center text-white/65 light:text-black/65 py-6 border border-dashed border-brand-main-600 rounded bg-brand-main-800/20">
                            No API keys configured
                          </div>
                        )}
                      </div>
                    </div>

                    {/* Endpoint */}
                    <div className="rounded border border-brand-main-600/60 bg-brand-main-800/30 p-4 space-y-3">
                      <div className="flex items-center justify-between">
                        <div>
                          <Label
                            htmlFor="baseUrl"
                            className="text-white light:text-brand-main-50"
                          >
                            API endpoint
                          </Label>
                          <p className="mt-0.5 text-xs text-white/55 light:text-black/55">
                            Override this only for a compatible proxy or private
                            endpoint.
                          </p>
                        </div>
                        {catalog?.baseUrl &&
                          customBaseUrl !== catalog.baseUrl && (
                            <button
                              type="button"
                              onClick={() => setCustomBaseUrl(catalog.baseUrl)}
                              className="text-xs text-blue-400 hover:text-blue-300 light:text-blue-600"
                            >
                              Use provider default
                            </button>
                          )}
                      </div>
                      <Input
                        id="baseUrl"
                        type="url"
                        value={customBaseUrl}
                        onChange={(e) => setCustomBaseUrl(e.target.value)}
                        placeholder={
                          catalog?.baseUrl || 'https://api.example.com/v1'
                        }
                        className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 placeholder:text-white/55 light:placeholder:text-black/55"
                        required
                      />
                      <div className="flex items-center gap-3 text-[11px] text-white/50 light:text-black/50">
                        {catalog?.apiVersion && (
                          <span>API version {catalog.apiVersion}</span>
                        )}
                        <span>
                          {catalog?.providerType === 'meta'
                            ? 'Runtime model discovery'
                            : 'Catalog-managed models'}
                        </span>
                      </div>
                    </div>

                    {/* Routing defaults */}
                    <div className="rounded border border-brand-main-600/60 bg-brand-main-800/30 p-4 space-y-4">
                      <div className="flex items-start gap-3">
                        <Checkbox
                          id="isDefaultProvider"
                          checked={isDefaultProvider}
                          onCheckedChange={() =>
                            setIsDefaultProvider(!isDefaultProvider)
                          }
                        />
                        <div className="min-w-0 flex-1">
                          <Label
                            htmlFor="isDefaultProvider"
                            className="text-white light:text-brand-main-50 cursor-pointer font-medium"
                          >
                            Use as the default provider
                          </Label>
                          <p className="text-xs text-white/50 light:text-black/50 mt-0.5">
                            Used when a request does not specify a provider
                            route.
                          </p>
                        </div>
                      </div>

                      <div className="space-y-2 border-t border-brand-main-600/40 pt-4">
                        <Label
                          htmlFor="defaultModel"
                          className="text-white light:text-brand-main-50 text-xs"
                        >
                          Default model
                        </Label>
                        <Select
                          value={defaultModel || '__none__'}
                          onValueChange={(value) =>
                            setDefaultModel(value === '__none__' ? '' : value)
                          }
                        >
                          <SelectTrigger
                            id="defaultModel"
                            className="w-full border-brand-main-500 bg-brand-main-700 text-white light:text-brand-main-50"
                          >
                            <SelectValue placeholder="No default model" />
                          </SelectTrigger>
                          <SelectContent className="border-brand-main-600 bg-brand-main-900 text-white">
                            <SelectItem value="__none__">
                              No default model
                            </SelectItem>
                            {Array.from(selectedModels).map((modelName) => (
                              <SelectItem key={modelName} value={modelName}>
                                {modelName}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                        {selectedModels.size === 0 && (
                          <p className="text-xs text-white/50 light:text-black/50">
                            Enable a model in the Models tab first.
                          </p>
                        )}
                      </div>
                    </div>

                    {/* Supported surface */}
                    {catalog?.capabilities && (
                      <div className="space-y-2">
                        <Label className="text-white light:text-brand-main-50">
                          Supported surface
                        </Label>
                        <div className="flex flex-wrap gap-1.5">
                          {Object.entries(catalog.capabilities)
                            .filter(([, supported]) => supported)
                            .map(([capability]) => (
                              <span
                                key={capability}
                                className="rounded border border-brand-main-500 bg-brand-main-800 px-2 py-1 text-[10px] capitalize text-white/65 light:text-black/65"
                              >
                                {capability.replaceAll('_', ' ')}
                              </span>
                            ))}
                        </div>
                      </div>
                    )}
                  </TabsContent>

                  <TabsContent
                    value="models"
                    className="mt-0 flex h-full min-h-0 flex-col gap-4"
                  >
                    {/* Model Selection */}
                    <div className="flex min-h-0 flex-1 flex-col gap-3">
                      <div className="flex items-center justify-between">
                        <Label className="text-white light:text-brand-main-50">
                          Enabled Models{' '}
                          <span className="text-red-400 light:text-red-600">
                            *
                          </span>
                        </Label>
                        <div className="flex gap-2 items-center">
                          <button
                            type="button"
                            onClick={selectAllModels}
                            className="text-xs text-blue-400 light:text-blue-600 hover:text-blue-300 light:hover:text-blue-600 transition-colors"
                          >
                            All
                          </button>
                          <span className="text-white/30 light:text-black/30">
                            |
                          </span>
                          <button
                            type="button"
                            onClick={deselectAllModels}
                            className="text-xs text-blue-400 light:text-blue-600 hover:text-blue-300 light:hover:text-blue-600 transition-colors"
                          >
                            None
                          </button>
                          {isMetaProvider && (
                            <>
                              <span className="text-white/30 light:text-black/30">
                                |
                              </span>
                              <button
                                type="button"
                                onClick={() => setDiscoveryDialogOpen(true)}
                                className="text-xs text-green-400 light:text-green-600 hover:text-green-300 light:hover:text-green-600 transition-colors flex items-center gap-1"
                              >
                                <Iconify.Icon
                                  icon="heroicons:plus-circle"
                                  className="size-3"
                                />
                                Discover
                              </button>
                            </>
                          )}
                          <span className="ml-2 text-xs text-white/60 light:text-black/60">
                            {selectedModels.size} selected
                          </span>
                        </div>
                      </div>

                      {/* Search Bar */}
                      <div className="relative">
                        <InputWithIcon
                          type="text"
                          placeholder="Search by model name or ID..."
                          value={modelSearchQuery}
                          onChange={(e) => setModelSearchQuery(e.target.value)}
                          icon={
                            <Iconify.Icon
                              icon="heroicons:magnifying-glass"
                              className="size-4 text-white/60 light:text-black/60"
                            />
                          }
                          iconPosition="left"
                          containerClassName="h-9 bg-brand-main-700"
                          className="text-sm"
                        />
                        {modelSearchQuery && (
                          <button
                            type="button"
                            onClick={() => setModelSearchQuery('')}
                            className="absolute right-3 top-1/2 -translate-y-1/2 text-white/60 light:text-black/60 hover:text-white/80 light:hover:text-black/80 transition-colors z-10"
                          >
                            <Iconify.Icon
                              icon="heroicons:x-mark"
                              className="size-4"
                            />
                          </button>
                        )}
                      </div>

                      <div className="min-h-[16rem] flex-1 overflow-y-auto rounded border border-brand-main-500 bg-brand-main-800/30 p-1 space-y-1 scrollbar-macos">
                        {/* Catalog Models */}
                        {filteredCatalogModels.map((model) => (
                          <div
                            key={`catalog-${model.name}`}
                            onClick={() => toggleModel(model.name)}
                            className={cn(
                              'group/model w-full flex items-start gap-3 p-2 rounded transition-all text-left cursor-pointer select-none',
                              selectedModels?.has(model.name)
                                ? 'bg-brand-secondary-500/10 border border-brand-secondary-500/30'
                                : 'hover:bg-brand-main-700 border border-transparent',
                            )}
                          >
                            <div className="flex items-center justify-center mt-0.5 pointer-events-none">
                              <div
                                className={cn(
                                  'size-4 rounded border-2 flex items-center justify-center transition-colors',
                                  selectedModels?.has(model.name)
                                    ? 'bg-brand-secondary-500 border-brand-secondary-500'
                                    : 'border-brand-main-400',
                                )}
                              >
                                {selectedModels?.has(model.name) && (
                                  <svg
                                    className="size-3 text-white light:text-brand-main-50"
                                    viewBox="0 0 12 12"
                                    fill="none"
                                    stroke="currentColor"
                                    strokeWidth="2"
                                  >
                                    <polyline points="2,6 5,9 10,3" />
                                  </svg>
                                )}
                              </div>
                            </div>
                            <div className="flex-1 min-w-0">
                              <div className="pointer-events-none">
                                <div className="flex items-center gap-1.5">
                                  <div className="text-white light:text-brand-main-50 font-medium text-sm truncate">
                                    {model.displayName}
                                  </div>

                                  {model.freshness === 'new' && (
                                    <span className="rounded bg-brand-secondary-500/15 px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wider text-brand-secondary-300">
                                      New
                                    </span>
                                  )}
                                  {model.status &&
                                    model.status !== 'stable' && (
                                      <span
                                        className={cn(
                                          'text-[9px] px-1.5 py-0.5 rounded uppercase tracking-wider font-medium',
                                          model.status === 'deprecated'
                                            ? 'bg-red-500/20 text-red-400 light:text-red-600'
                                            : model.status === 'beta'
                                              ? 'bg-yellow-500/20 text-yellow-400 light:text-yellow-700'
                                              : model.status === 'preview'
                                                ? 'bg-blue-500/20 text-blue-400 light:text-blue-600'
                                                : 'bg-white/10 light:bg-black/10 text-white/60 light:text-black/60',
                                        )}
                                      >
                                        {model.status}
                                      </span>
                                    )}
                                  {model.variants.length > 0 && (
                                    <span
                                      className="max-w-48 truncate rounded bg-violet-500/15 px-1.5 py-0.5 text-[9px] text-violet-300"
                                      title={model.variants
                                        .map((variant) => variant.description)
                                        .join('\n')}
                                    >
                                      {model.variants
                                        .map((variant) => variant.id)
                                        .join(' · ')}
                                    </span>
                                  )}
                                  {model.structuredOutput && (
                                    <span className="rounded bg-cyan-500/10 px-1.5 py-0.5 text-[9px] text-cyan-300">
                                      Structured
                                    </span>
                                  )}
                                  <button
                                    type="button"
                                    onClick={(event) => {
                                      event.stopPropagation()
                                      handleCopy(model.name)
                                    }}
                                    className="invisible group-hover/model:visible rounded p-1 pointer-events-auto hover:bg-white/10 light:hover:bg-black/10"
                                    title="Copy model name"
                                  >
                                    <Iconify.Icon
                                      icon="heroicons:clipboard-document"
                                      className="size-3.5 text-white/60 light:text-black/60"
                                    />
                                  </button>
                                </div>
                                <div className="flex items-center justify-between gap-2">
                                  <p className="text-xs text-white/65 light:text-black/65 truncate font-mono">
                                    {model.name}
                                  </p>
                                  <div className="flex gap-2 text-[10px] text-white/55 light:text-black/55 whitespace-nowrap">
                                    {model.maxOutputTokens > 0 && (
                                      <span>
                                        {model.maxOutputTokens.toLocaleString()}{' '}
                                        output
                                      </span>
                                    )}
                                    {model.maxTokens > 0 && (
                                      <span>
                                        {model.maxTokens.toLocaleString()} ctx
                                      </span>
                                    )}
                                  </div>
                                </div>
                              </div>

                              {selectedModels.has(model.name) &&
                                model.variants.length > 0 && (
                                  <div
                                    className="mt-2 flex items-center gap-2 border-t border-brand-secondary-500/20 pt-2"
                                    onClick={(event) => event.stopPropagation()}
                                  >
                                    <span className="text-[10px] uppercase tracking-wider text-white/50 light:text-black/50">
                                      Variant
                                    </span>
                                    <Select
                                      value={selectedVariantFor(model)}
                                      onValueChange={(value) =>
                                        setModelVariant(model, value)
                                      }
                                    >
                                      <SelectTrigger className="h-7 flex-1 border-brand-main-500 bg-brand-main-700 text-xs text-white light:text-brand-main-50">
                                        <SelectValue />
                                      </SelectTrigger>
                                      <SelectContent className="border-brand-main-600 bg-brand-main-900 text-white">
                                        <SelectItem value="__default__">
                                          Provider default
                                        </SelectItem>
                                        {model.variants.map((variant) => (
                                          <SelectItem
                                            key={variant.id}
                                            value={variant.id}
                                          >
                                            {variant.displayName}
                                          </SelectItem>
                                        ))}
                                      </SelectContent>
                                    </Select>
                                  </div>
                                )}
                            </div>
                          </div>
                        ))}

                        {/* Custom Models */}
                        {filteredCustomModels.length > 0 &&
                          filteredCatalogModels.length > 0 && (
                            <div className="px-2 pt-2 pb-1 text-[10px] uppercase tracking-[0.18em] text-white/60 light:text-black/60">
                              Custom Models
                            </div>
                          )}
                        {filteredCustomModels.length > 0 &&
                          filteredCustomModels.map((model) => (
                            <div
                              key={`custom-${model.modelName}`}
                              onClick={() => toggleModel(model.modelName)}
                              className={cn(
                                'group/model w-full flex items-start gap-3 p-2 rounded transition-all text-left cursor-pointer select-none',
                                selectedModels?.has(model.modelName)
                                  ? 'bg-green-600/10 border border-green-500/30'
                                  : 'hover:bg-brand-main-700 border border-transparent',
                              )}
                            >
                              <div className="flex items-center justify-center mt-0.5 pointer-events-none">
                                <div
                                  className={cn(
                                    'size-4 rounded border-2 flex items-center justify-center transition-colors',
                                    selectedModels?.has(model.modelName)
                                      ? 'bg-green-600 border-green-600'
                                      : 'border-brand-main-400',
                                  )}
                                >
                                  {selectedModels?.has(model.modelName) && (
                                    <svg
                                      className="size-3 text-white light:text-brand-main-50"
                                      viewBox="0 0 12 12"
                                      fill="none"
                                      stroke="currentColor"
                                      strokeWidth="2"
                                    >
                                      <polyline points="2,6 5,9 10,3" />
                                    </svg>
                                  )}
                                </div>
                              </div>
                              <div className="flex-1 min-w-0 pointer-events-none">
                                <div className="text-white light:text-brand-main-50 font-medium text-sm flex items-center gap-1.5">
                                  {model.displayName}
                                  <span className="text-[9px] px-1 py-0 rounded bg-green-500/20 text-green-400 light:text-green-600 uppercase tracking-wider">
                                    Custom
                                  </span>
                                  <button
                                    type="button"
                                    onClick={(e) => {
                                      e.stopPropagation()
                                      handleCopy(model.modelName)
                                    }}
                                    className="invisible group-hover/model:visible p-1 hover:bg-white/20 light:hover:bg-black/20 rounded flex-shrink-0 transition-all pointer-events-auto"
                                    title="Copy model name"
                                  >
                                    <Iconify.Icon
                                      icon="heroicons:clipboard-document"
                                      className="size-3.5 text-white/70 light:text-black/70 hover:text-white light:hover:text-brand-main-50"
                                    />
                                  </button>
                                </div>
                                <p className="text-xs text-white/65 light:text-black/65 truncate font-mono">
                                  {model.modelName}
                                </p>
                              </div>
                            </div>
                          ))}

                        {/* No Results Message */}
                        {filteredCatalogModels.length === 0 &&
                          filteredCustomModels.length === 0 && (
                            <div className="flex flex-col min-h-52 items-center justify-center py-8 text-white/60 light:text-black/60">
                              <Iconify.Icon
                                icon="heroicons:magnifying-glass"
                                className="size-8 mb-2 opacity-50"
                              />
                              <p className="text-sm">No models found</p>
                              {modelSearchQuery && (
                                <p className="text-xs mt-1">
                                  Try a different search term
                                </p>
                              )}
                            </div>
                          )}
                      </div>
                      {defaultModel && (
                        <p className="text-xs text-white/65 light:text-black/65 text-right">
                          Default model:{' '}
                          <span className="text-white/60 light:text-black/60">
                            {defaultModel}
                          </span>
                        </p>
                      )}
                    </div>
                  </TabsContent>

                  <TabsContent
                    value="parameters"
                    className="space-y-5 mt-0 h-full overflow-y-auto scrollbar-macos"
                  >
                    <div className="rounded border border-blue-500/20 bg-blue-500/10 p-4 text-sm text-blue-200 light:text-blue-700">
                      <div className="flex items-start gap-3">
                        <Iconify.Icon
                          icon="heroicons:information-circle"
                          className="size-5 shrink-0 mt-0.5"
                        />
                        <p className="flex-1">
                          Provider defaults apply to every model that does not
                          set the same control itself, and only to the models
                          that accept it. A model overrides them control by
                          control, and a request overrides both.
                        </p>
                      </div>
                    </div>

                    {selectedModels.size === 0 ? (
                      <div className="flex min-h-56 flex-col items-center justify-center rounded border border-dashed border-brand-main-600 text-center">
                        <Iconify.Icon
                          icon="heroicons:cube-transparent"
                          className="mb-2 size-7 text-white/35 light:text-black/35"
                        />
                        <p className="text-sm text-white/65 light:text-black/65">
                          No models enabled
                        </p>
                        <button
                          type="button"
                          onClick={() => setActiveTab('models')}
                          className="mt-1 text-xs text-blue-400 light:text-blue-600"
                        >
                          Choose models
                        </button>
                      </div>
                    ) : (
                      <>
                        <div className="space-y-3">
                          <div className="flex items-center justify-between">
                            <Label className="text-white light:text-brand-main-50">
                              Provider defaults
                            </Label>
                            {Object.keys(providerParameterValues).length >
                              0 && (
                              <button
                                type="button"
                                onClick={resetProviderDefaults}
                                className="text-xs text-white/55 hover:text-white/80 light:text-black/55 light:hover:text-black/80"
                              >
                                Reset provider
                              </button>
                            )}
                          </div>

                          {providerParameters.length > 0 ? (
                            <div className="space-y-3">
                              {providerParameters.map((parameter) => (
                                <ParameterControl
                                  key={parameter.key}
                                  parameter={parameter}
                                  value={
                                    providerParameterValues[parameter.key] || ''
                                  }
                                  onChange={(value) =>
                                    setProviderParameter(parameter.key, value)
                                  }
                                  fallbackLabel="Provider default"
                                  setLabel="Applies to all models"
                                />
                              ))}
                            </div>
                          ) : (
                            <div className="rounded border border-dashed border-brand-main-600 p-6 text-center">
                              <p className="text-sm text-white/60 light:text-black/60">
                                No catalog-backed controls for these models.
                              </p>
                              <p className="mt-1 text-xs text-white/40 light:text-black/40">
                                Pass parameters with each request instead.
                              </p>
                            </div>
                          )}
                        </div>

                        <div className="h-px bg-brand-main-600/60" />

                        <div className="space-y-2">
                          <div className="flex items-center justify-between">
                            <Label className="text-white light:text-brand-main-50">
                              Configure model
                            </Label>
                            {parameterModel &&
                              modelParameterValues[parameterModel] && (
                                <button
                                  type="button"
                                  onClick={resetParameterDefaults}
                                  className="text-xs text-white/55 hover:text-white/80 light:text-black/55 light:hover:text-black/80"
                                >
                                  Reset model
                                </button>
                              )}
                          </div>
                          <Select
                            value={
                              parameterModel ||
                              Array.from(selectedModels)[0] ||
                              ''
                            }
                            onValueChange={setParameterModel}
                          >
                            <SelectTrigger className="w-full border-brand-main-500 bg-brand-main-700 text-white light:text-brand-main-50">
                              <SelectValue placeholder="Select a model" />
                            </SelectTrigger>
                            <SelectContent className="border-brand-main-600 bg-brand-main-900 text-white">
                              {Array.from(selectedModels).map((modelName) => (
                                <SelectItem key={modelName} value={modelName}>
                                  {modelName}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </div>

                        {selectedParameterModel ? (
                          <>
                            <div className="flex flex-wrap items-center gap-2 rounded border border-brand-main-600/60 bg-brand-main-800/30 p-3 text-[11px] text-white/55 light:text-black/55">
                              <span>
                                {selectedParameterModel.maxTokens.toLocaleString()}{' '}
                                context
                              </span>
                              {selectedParameterModel.maxOutputTokens > 0 && (
                                <>
                                  <span className="text-white/20">•</span>
                                  <span>
                                    {selectedParameterModel.maxOutputTokens.toLocaleString()}{' '}
                                    max output
                                  </span>
                                </>
                              )}
                              {selectedParameterModel.structuredOutput && (
                                <>
                                  <span className="text-white/20">•</span>
                                  <span>Structured output</span>
                                </>
                              )}
                              {selectedParameterModel.inputModalities.map(
                                (modality) => (
                                  <span
                                    key={modality}
                                    className="rounded bg-white/5 px-1.5 py-0.5 capitalize light:bg-black/5"
                                  >
                                    {modality}
                                  </span>
                                ),
                              )}
                            </div>

                            {selectedParameterModel.parameters.length > 0 ? (
                              <div className="space-y-3">
                                {selectedParameterModel.parameters.map(
                                  (parameter) => (
                                    <ParameterControl
                                      key={parameter.key}
                                      parameter={parameter}
                                      value={
                                        modelParameterValues[parameterModel]?.[
                                          parameter.key
                                        ] || ''
                                      }
                                      onChange={(value) =>
                                        setModelParameter(
                                          parameterModel,
                                          parameter.key,
                                          value,
                                        )
                                      }
                                      fallbackLabel={
                                        providerParameterValues[parameter.key]
                                          ? 'Provider default'
                                          : 'Catalog default'
                                      }
                                      setLabel="Model override"
                                    />
                                  ),
                                )}
                              </div>
                            ) : (
                              <div className="rounded border border-dashed border-brand-main-600 p-8 text-center">
                                <p className="text-sm text-white/60 light:text-black/60">
                                  No catalog-backed defaults are available for
                                  this model.
                                </p>
                                <p className="mt-1 text-xs text-white/40 light:text-black/40">
                                  Pass model-specific parameters with each
                                  request.
                                </p>
                              </div>
                            )}
                          </>
                        ) : (
                          <div className="rounded border border-dashed border-brand-main-600 p-8 text-center text-sm text-white/60 light:text-black/60">
                            This custom model has no parameter metadata yet.
                          </div>
                        )}
                      </>
                    )}
                  </TabsContent>
                </div>
              </Tabs>

              <SheetFooter className="flex items-center justify-center px-6 py-2 mt-auto w-full">
                <div className="flex items-center justify-end gap-2 w-full">
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => onOpenChange(false)}
                    className="text-white light:text-brand-main-50 w-1/2 hover:bg-brand-main-800"
                  >
                    Cancel
                  </Button>
                  <Button
                    type="submit"
                    className="w-1/2"
                    disabled={
                      configureProviderMutation.isPending ||
                      !providerConfigId ||
                      selectedModels.size === 0
                    }
                  >
                    {configureProviderMutation.isPending ? (
                      <>
                        <Iconify.Icon
                          icon="heroicons:arrow-path"
                          className="size-4 animate-spin mr-2"
                        />
                        Saving...
                      </>
                    ) : (
                      <>
                        {/* <Iconify.Icon icon="heroicons:check" className="size-4 " /> */}
                        Save Changes
                      </>
                    )}
                  </Button>
                </div>
              </SheetFooter>
            </form>
          )}
        </SheetContent>
      </Sheet>

      {/* Model Discovery Dialog for Meta-Providers */}
      {isMetaProvider && providerName && (
        <ModelDiscoveryDialog
          open={discoveryDialogOpen}
          onOpenChange={setDiscoveryDialogOpen}
          providerName={providerName}
          providerDisplayName={catalog?.displayName || providerName}
          supportsDiscovery={supportsDiscovery}
          customBaseUrl={customBaseUrl}
        />
      )}
    </>
  )
}
