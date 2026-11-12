<template>
  <t-drawer :visible="visible" :header="$t('knowledgeEditor.wikiBrowser.generatedQuestionsTitle')"
    size="min(960px, 94vw)" placement="right" :footer="false" destroy-on-close
    @update:visible="emit('update:visible', $event)">
    <div class="generated-questions-drawer">
      <div class="generated-questions-note">
        {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsScope') }}
        <br />
        {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsHistoryLimit') }}
        <br />
        {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsTrainingExportHint') }}
      </div>

      <div class="generated-questions-filters">
        <t-input v-model="filters.keyword" clearable
          :placeholder="$t('knowledgeEditor.wikiBrowser.generatedQuestionsKeyword')" @enter="applyFilters">
          <template #prefixIcon><t-icon name="search" /></template>
        </t-input>
        <t-input v-model="filters.knowledge_id" clearable
          :placeholder="$t('knowledgeEditor.wikiBrowser.generatedQuestionsKnowledgeId')" @enter="applyFilters" />
        <t-input v-model="filters.chunk_id" clearable
          :placeholder="$t('knowledgeEditor.wikiBrowser.generatedQuestionsChunkId')" @enter="applyFilters" />
        <t-select v-model="filters.duplicate_mode" :options="duplicateOptions"
          :placeholder="$t('knowledgeEditor.wikiBrowser.generatedQuestionsDuplicateMode')" />
        <t-select v-model="filters.answer_state" :options="answerStateOptions"
          :placeholder="$t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerState')" />
        <t-select v-model="filters.sort_by" :options="sortOptions"
          :placeholder="$t('knowledgeEditor.wikiBrowser.generatedQuestionsSortBy')" />
        <t-select v-model="filters.sort_order" :options="sortOrderOptions" />
        <t-button theme="primary" :loading="loading" @click="applyFilters">
          {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsFilter') }}
        </t-button>
      </div>

      <div class="generated-questions-actions">
        <div class="generated-questions-count">
          {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsCount', { count: total, raw: rawTotal }) }}
        </div>
        <div class="generated-questions-export-actions">
          <t-button variant="outline" :disabled="loading || rows.length === 0" :loading="copying"
            @click="copyCurrentPage">
            {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsCopyPage') }}
          </t-button>
          <t-button variant="outline" :disabled="loading || total === 0" :loading="copyingFiltered"
            @click="copyFiltered">
            {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsCopyFiltered') }}
          </t-button>
          <t-button variant="outline" :disabled="loading || total === 0" :loading="exporting"
            @click="downloadExport('csv')">
            {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsExportCSV') }}
          </t-button>
          <t-button variant="outline" :disabled="loading || total === 0" :loading="exporting"
            @click="downloadExport('json')">
            {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsExportJSON') }}
          </t-button>
          <t-button variant="outline" :disabled="loading || total === 0" :loading="exporting"
            @click="downloadExport('training_jsonl')">
            {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsExportTrainingCorpus') }}
          </t-button>
          <t-button theme="primary" :disabled="loading || total === 0 || answerBatch.running"
            :loading="answerBatch.running" @click="generateFilteredAnswers">
            {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsGenerateFilteredAnswers') }}
          </t-button>
        </div>
      </div>

      <div v-if="answerBatch.running" class="generated-questions-answer-progress" role="status">
        <div class="generated-questions-answer-progress-heading">
          <span>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsGeneratingAnswers', {
            done: answerBatch.done, total: answerBatch.total || '…'
          }) }}</span>
          <t-button size="small" variant="text" @click="stopAnswerGeneration">
            {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsStopGeneration') }}
          </t-button>
        </div>
        <progress :max="Math.max(answerBatch.total, 1)" :value="answerBatch.done" />
        <div class="generated-questions-answer-progress-counts">
          {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsGenerationSummary', {
            answered: answerBatch.answered,
            partial: answerBatch.partialEvidence,
            insufficient: answerBatch.insufficient,
            unavailable: answerBatch.unavailable,
            failed: answerBatch.failed
          }) }}
        </div>
      </div>

      <div v-if="dataWarnings > 0" class="generated-questions-warning" role="status">
        {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsDataWarning', { count: dataWarnings }) }}
      </div>
      <div v-if="errorMessage" class="generated-questions-error" role="alert">
        <span>{{ errorMessage }}</span>
        <t-button size="small" variant="text" @click="loadPage(page)">
          {{ $t('common.retry') }}
        </t-button>
      </div>

      <div v-if="loading" class="generated-questions-state">
        <t-loading />
      </div>
      <div v-else-if="rows.length === 0 && !errorMessage" class="generated-questions-state">
        {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsEmpty') }}
      </div>
      <div v-else-if="rows.length > 0" class="generated-questions-table-wrap">
        <table class="generated-questions-table">
          <thead>
            <tr>
              <th>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsQuestion') }}</th>
              <th>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsSource') }}</th>
              <th>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsChunk') }}</th>
              <th>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsStatus') }}</th>
              <th>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerState') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in rows" :key="row.row_key">
              <td class="question-cell">
                <div class="question-text">
                  {{ row.question || $t('knowledgeEditor.wikiBrowser.generatedQuestionsMissingText') }}
                </div>
                <div class="question-meta">
                  <span>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsQuestionId') }}: {{ row.question_id || '—' }}</span>
                  <span>#{{ row.question_position }}</span>
                  <t-tag v-if="row.is_duplicate" size="small" theme="warning" variant="light">
                    {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsDuplicateCount', { count: row.duplicate_count }) }}
                  </t-tag>
                </div>
                <t-button size="small" variant="text" class="copy-question-button"
                  :loading="copyingRows[row.row_key]" @click="copyQuestionAndSource(row)">
                  {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsCopyQuestionAndSource') }}
                </t-button>
                <t-button size="small" variant="text" class="source-original-toggle"
                  :loading="sourceCache[row.chunk_id]?.loading" @click="toggleSourceOriginal(row)">
                  {{ expandedSources[row.row_key]
                    ? $t('knowledgeEditor.wikiBrowser.generatedQuestionsHideSource')
                    : $t('knowledgeEditor.wikiBrowser.generatedQuestionsShowSource') }}
                </t-button>
                <div v-if="expandedSources[row.row_key]" class="source-original-panel">
                  <div v-if="sourceCache[row.chunk_id]?.loading" class="source-original-state">
                    <t-loading size="small" />
                    <span>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsSourceLoading') }}</span>
                  </div>
                  <div v-else-if="sourceCache[row.chunk_id]?.error" class="source-original-error">
                    <span>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsSourceReadFailed') }}</span>
                    <t-button size="small" variant="text" @click="loadSourceOriginal(row, true)">
                      {{ $t('common.retry') }}
                    </t-button>
                  </div>
                  <template v-else-if="sourceCache[row.chunk_id]?.loaded">
                    <div class="source-original-heading">
                      <span>{{ sourceRevisionLabel(row) }}</span>
                      <t-button v-if="sourceCache[row.chunk_id]?.historyError && row.question_revision !== null"
                        size="small" variant="text" @click="loadSourceOriginal(row, true)">
                        {{ $t('common.retry') }}
                      </t-button>
                    </div>
                    <div class="source-answer-panel">
                      <div class="source-answer-heading">
                        {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswer') }}
                      </div>
                      <div v-if="answerCache[row.row_key]?.loading" class="source-original-state">
                        <t-loading size="small" />
                        <span>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerLoading') }}</span>
                      </div>
                      <div v-else-if="answerCache[row.row_key]?.error" class="source-original-error">
                        <span>{{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerFailed') }}</span>
                        <t-button size="small" variant="text" @click="loadQuestionAnswer(row, true)">
                          {{ $t('common.retry') }}
                        </t-button>
                      </div>
                      <div v-else-if="answerCache[row.row_key]?.loaded">
                        <pre v-if="answerCache[row.row_key]?.answer" class="source-answer-text">{{ answerCache[row.row_key].answer }}</pre>
                        <div v-else class="source-original-empty">
                          {{ answerEmptyMessage(answerCache[row.row_key]?.status || '') }}
                        </div>
                      </div>
                    </div>
                    <pre v-if="sourceVersionAvailable(row) && sourceContentForRow(row)"
                      class="source-original-text">{{ sourceContentForRow(row) }}</pre>
                    <div v-else-if="sourceVersionAvailable(row)" class="source-original-empty">
                      {{ $t('knowledgeEditor.wikiBrowser.generatedQuestionsSourceEmpty') }}
                    </div>
                  </template>
                </div>
              </td>
              <td class="source-cell">
                <div class="source-title">{{ row.knowledge_title || row.knowledge_id }}</div>
                <div class="source-id">{{ row.knowledge_id }}</div>
              </td>
              <td class="chunk-cell">
                <div>#{{ row.chunk_index }}</div>
                <div class="source-id">{{ row.chunk_id }}</div>
              </td>
              <td class="status-cell">
                <t-tag size="small" :theme="revisionTheme(row.revision_state)" variant="light">
                  {{ revisionLabel(row.revision_state) }}
                </t-tag>
                <t-tag size="small" :theme="row.chunk_availability === 'available' ? 'success' : 'default'"
                  variant="light">
                  {{ row.chunk_availability === 'available'
                    ? $t('knowledgeEditor.wikiBrowser.generatedQuestionsAvailable')
                    : $t('knowledgeEditor.wikiBrowser.generatedQuestionsDisabled') }}
                </t-tag>
                <span v-if="row.index_status" class="index-status">{{ row.index_status }}</span>
              </td>
              <td class="answer-status-cell">
                <t-tag size="small" :theme="answerStateTheme(row.answer_state)" variant="light"
                  :title="(row.answer_updated_at || row.answer_created_at)
                    ? $t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerGeneratedAt', {
                      time: formatAnswerCreatedAt(row.answer_updated_at || row.answer_created_at || '')
                    })
                    : undefined">
                  {{ answerStateLabel(row.answer_state) }}
                </t-tag>
                <div v-if="row.answer_updated_at || row.answer_created_at" class="answer-generated-at">
                  {{ formatAnswerCreatedAt(row.answer_updated_at || row.answer_created_at || '') }}
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="total > 0" class="generated-questions-pagination">
        <t-pagination :current="page" :page-size="pageSize" :total="total" :page-size-options="[20, 50, 100]"
          show-jumper show-page-size @change="handlePaginationChange" />
      </div>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  extractGeneratedQuestionAnswer,
  exportGeneratedQuestions,
  getChunkByIdOnly,
  listChunkRevisions,
  listGeneratedQuestions,
  type GeneratedQuestionAnswerState,
  type GeneratedQuestionDuplicateMode,
  type GeneratedQuestionListItem,
  type GeneratedQuestionListParams,
} from '@/api/knowledge-base'

const props = defineProps<{
  visible: boolean
  kbId: string
}>()
const emit = defineEmits<{
  (event: 'update:visible', value: boolean): void
}>()
const { t } = useI18n()

const filters = reactive<GeneratedQuestionListParams>({
  duplicate_mode: 'all',
  answer_state: 'all',
  sort_by: 'chunk_index',
  sort_order: 'asc',
})
const rows = ref<GeneratedQuestionListItem[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const rawTotal = ref(0)
const dataWarnings = ref(0)
const loading = ref(false)
const copying = ref(false)
const copyingFiltered = ref(false)
const exporting = ref(false)
const errorMessage = ref('')
const answerBatch = reactive({
  running: false,
  stopRequested: false,
  total: 0,
  done: 0,
  answered: 0,
  partialEvidence: 0,
  insufficient: 0,
  unavailable: 0,
  failed: 0,
})
const expandedSources = reactive<Record<string, boolean>>({})
const copyingRows = reactive<Record<string, boolean>>({})

interface GeneratedQuestionSourceCache {
  loading: boolean
  loaded: boolean
  error: boolean
  historyError: boolean
  currentContent: string
  currentRevision: number | null
  revisions: Array<{ revision: number; content: string }>
}

const sourceCache = reactive<Record<string, GeneratedQuestionSourceCache>>({})
const sourceLoadPromises = new Map<string, Promise<void>>()

interface GeneratedQuestionAnswerCache {
  loading: boolean
  loaded: boolean
  error: boolean
  answer: string
  status: string
  modelId: string
}

const answerCache = reactive<Record<string, GeneratedQuestionAnswerCache>>({})
const answerLoadPromises = new Map<string, Promise<void>>()

const duplicateOptions = computed(() => [
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsAll'), value: 'all' },
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsDuplicates'), value: 'duplicates' },
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsUnique'), value: 'unique' },
] as Array<{ label: string; value: GeneratedQuestionDuplicateMode }>)

const answerStateOptions = computed(() => [
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerAll'), value: 'all' },
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerNotGenerated'), value: 'not_generated' },
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerGenerated'), value: 'answered' },
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerPartial'), value: 'partial_evidence' },
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerInsufficient'), value: 'insufficient_source' },
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerNotFoundState'), value: 'not_found' },
] as Array<{ label: string; value: GeneratedQuestionAnswerState }>)

const sortOptions = computed(() => [
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsSortChunkIndex'), value: 'chunk_index' },
  { label: t('knowledgeEditor.wikiBrowser.generatedQuestionsSortQuestionId'), value: 'question_id' },
])
const sortOrderOptions = [
  { label: '↑', value: 'asc' },
  { label: '↓', value: 'desc' },
]

watch(() => props.visible, (visible) => {
  if (visible) {
    page.value = 1
    void loadPage(1)
  }
})

function requestParams(requestPage = page.value): GeneratedQuestionListParams {
  return {
    ...filters,
    page: requestPage,
    page_size: pageSize.value,
  }
}

async function loadPage(requestPage = 1) {
  if (!props.visible) return
  loading.value = true
  errorMessage.value = ''
  try {
    const response = await listGeneratedQuestions(props.kbId, requestParams(requestPage)) as any
    const body = response?.data && !Array.isArray(response.data) ? response.data : response
    rows.value = Array.isArray(body?.data) ? body.data : []
    total.value = Number(body?.total || 0)
    rawTotal.value = Number(body?.raw_total || 0)
    dataWarnings.value = Number(body?.data_warnings || 0)
    page.value = Number(body?.page || requestPage)
    pageSize.value = Number(body?.page_size || pageSize.value)
  } catch {
    rows.value = []
    total.value = 0
    rawTotal.value = 0
    dataWarnings.value = 0
    errorMessage.value = t('knowledgeEditor.wikiBrowser.generatedQuestionsReadFailed')
  } finally {
    loading.value = false
  }
}

function applyFilters() {
  page.value = 1
  void loadPage(1)
}

function handlePaginationChange(info: { current?: number; pageSize?: number }) {
  if (info.pageSize && info.pageSize !== pageSize.value) pageSize.value = info.pageSize
  const nextPage = info.current || 1
  page.value = nextPage
  void loadPage(nextPage)
}

function revisionTheme(state: GeneratedQuestionListItem['revision_state']) {
  if (state === 'current') return 'success'
  if (state === 'stale') return 'warning'
  return 'default'
}

function revisionLabel(state: GeneratedQuestionListItem['revision_state']) {
  const key = state === 'current'
    ? 'generatedQuestionsCurrent'
    : state === 'stale'
      ? 'generatedQuestionsStale'
      : 'generatedQuestionsUnknown'
  return t(`knowledgeEditor.wikiBrowser.${key}`)
}

function answerStateLabel(state: GeneratedQuestionListItem['answer_state']) {
  const key = state === 'answered'
    ? 'generatedQuestionsAnswerGenerated'
    : state === 'partial_evidence'
      ? 'generatedQuestionsAnswerPartial'
      : state === 'insufficient_source'
        ? 'generatedQuestionsAnswerInsufficient'
        : state === 'not_found'
          ? 'generatedQuestionsAnswerNotFoundState'
          : 'generatedQuestionsAnswerNotGenerated'
  return t(`knowledgeEditor.wikiBrowser.${key}`)
}

function answerStateTheme(state: GeneratedQuestionListItem['answer_state']) {
  if (state === 'answered') return 'success'
  if (state === 'partial_evidence' || state === 'insufficient_source' || state === 'not_found') return 'warning'
  return 'default'
}

function answerEmptyMessage(status: string) {
  if (status === 'insufficient_source') {
    return t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerInsufficient')
  }
  if (status === 'not_found') return t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerNotFound')
  if (status === 'partial_evidence') {
    return t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerPartial')
  }
  return t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerUnavailable')
}

function formatAnswerCreatedAt(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function sourceLine(row: GeneratedQuestionListItem) {
  const documentName = row.knowledge_title || row.knowledge_id
  const questionId = row.question_id || '—'
  return `${row.question}\n[document=${documentName}; knowledge_id=${row.knowledge_id}; chunk_index=${row.chunk_index}; chunk_id=${row.chunk_id}; question_id=${questionId}]`
}

async function toggleSourceOriginal(row: GeneratedQuestionListItem) {
  expandedSources[row.row_key] = !expandedSources[row.row_key]
  if (expandedSources[row.row_key]) {
    await loadSourceOriginal(row)
    if (sourceVersionAvailable(row) && sourceContentForRow(row).trim()) {
      await loadQuestionAnswer(row)
    } else {
      answerCache[row.row_key] = {
        loading: false, loaded: true, error: false, answer: '', status: 'source_unavailable', modelId: '',
      }
    }
  }
}

async function loadSourceOriginal(row: GeneratedQuestionListItem, force = false) {
  const existing = sourceCache[row.chunk_id]
  const inFlight = sourceLoadPromises.get(row.chunk_id)
  if (inFlight) {
    await inFlight
    return
  }
  if (existing?.loaded && !existing.error && !existing.historyError && !force) return

  sourceCache[row.chunk_id] = {
    loading: true,
    loaded: false,
    error: false,
    historyError: false,
    currentContent: '',
    currentRevision: null,
    revisions: [],
  }

  const request = (async () => {
    try {
      const chunkResponse = await getChunkByIdOnly(row.chunk_id) as any
      const chunk = chunkResponse?.data
      if (!chunk || chunk.id !== row.chunk_id || chunk.knowledge_id !== row.knowledge_id
        || chunk.knowledge_base_id !== props.kbId) {
        throw new Error('Source chunk did not match the selected knowledge base')
      }

      const currentRevisionValue = Number(chunk.content_revision)
      const state = sourceCache[row.chunk_id]
      state.currentContent = String(chunk.content || '')
      state.currentRevision = Number.isFinite(currentRevisionValue) ? currentRevisionValue : null

      try {
        const revisionsResponse = await listChunkRevisions(row.knowledge_id, row.chunk_id) as any
        const revisions = Array.isArray(revisionsResponse?.data) ? revisionsResponse.data : []
        state.revisions = revisions
          .filter((revision: any) => Number.isFinite(Number(revision?.revision))
            && typeof revision?.content === 'string')
          .map((revision: any) => ({ revision: Number(revision.revision), content: revision.content }))
      } catch {
        state.historyError = true
      }
      state.loaded = true
    } catch {
      sourceCache[row.chunk_id].error = true
      sourceCache[row.chunk_id].loaded = true
    } finally {
      sourceCache[row.chunk_id].loading = false
    }
  })()

  sourceLoadPromises.set(row.chunk_id, request)
  try {
    await request
  } finally {
    if (sourceLoadPromises.get(row.chunk_id) === request) {
      sourceLoadPromises.delete(row.chunk_id)
    }
  }
}

function sourceVersionAvailable(row: GeneratedQuestionListItem) {
  const state = sourceCache[row.chunk_id]
  if (!state?.loaded || state.error) return false
  if (row.question_revision === null) return true
  if (state.currentRevision === row.question_revision) return true
  return state.revisions.some((revision) => revision.revision === row.question_revision)
}

function sourceContentForRow(row: GeneratedQuestionListItem) {
  const state = sourceCache[row.chunk_id]
  if (!state?.loaded || state.error) return ''
  if (row.question_revision === null || state.currentRevision === row.question_revision) {
    return state.currentContent
  }
  return state.revisions.find((revision) => revision.revision === row.question_revision)?.content || ''
}

function sourceRevisionLabel(row: GeneratedQuestionListItem) {
  const state = sourceCache[row.chunk_id]
  if (row.question_revision === null) {
    return t('knowledgeEditor.wikiBrowser.generatedQuestionsSourceUnknown')
  }
  if (state?.currentRevision === row.question_revision) {
    return t('knowledgeEditor.wikiBrowser.generatedQuestionsSourceCurrent', { revision: row.question_revision })
  }
  if (state?.revisions.some((revision) => revision.revision === row.question_revision)) {
    return t('knowledgeEditor.wikiBrowser.generatedQuestionsSourceHistorical', { revision: row.question_revision })
  }
  return state?.historyError
    ? t('knowledgeEditor.wikiBrowser.generatedQuestionsSourceHistoryReadFailed')
    : t('knowledgeEditor.wikiBrowser.generatedQuestionsSourceUnavailable')
}

async function loadQuestionAnswer(row: GeneratedQuestionListItem, force = false) {
  const existing = answerCache[row.row_key]
  const inFlight = answerLoadPromises.get(row.row_key)
  if (inFlight) {
    await inFlight
    return
  }
  if (existing?.loaded && !existing.error && !force) return

  answerCache[row.row_key] = {
    loading: true, loaded: false, error: false, answer: '', status: '', modelId: '',
  }
  const request = (async () => {
    try {
      const response = await extractGeneratedQuestionAnswer(props.kbId, {
        chunk_id: row.chunk_id,
        question_id: row.question_id,
        question_position: row.question_position,
      }) as any
      const data = response?.data?.data ?? response?.data ?? response
      answerCache[row.row_key].answer = String(data?.answer || '')
      answerCache[row.row_key].status = String(data?.answer_status || 'source_unavailable')
      answerCache[row.row_key].modelId = String(data?.answer_model_id || '')
      if (['answered', 'partial_evidence', 'insufficient_source', 'not_found'].includes(data?.answer_status)) {
        row.answer_state = data.answer_status === 'not_found' ? 'insufficient_source' : data.answer_status
        row.answer_created_at = data.answer_created_at || row.answer_created_at
        row.answer_updated_at = data.answer_updated_at || row.answer_updated_at
        row.answer_model_id = String(data?.answer_model_id || '')
      }
      answerCache[row.row_key].loaded = true
    } catch {
      answerCache[row.row_key].error = true
      answerCache[row.row_key].loaded = true
    } finally {
      answerCache[row.row_key].loading = false
    }
  })()
  answerLoadPromises.set(row.row_key, request)
  try {
    await request
  } finally {
    if (answerLoadPromises.get(row.row_key) === request) {
      answerLoadPromises.delete(row.row_key)
    }
  }
}

function stopAnswerGeneration() {
  answerBatch.stopRequested = true
}

async function generateFilteredAnswers() {
  if (answerBatch.running) return

  answerBatch.running = true
  answerBatch.stopRequested = false
  answerBatch.total = 0
  answerBatch.done = 0
  answerBatch.answered = 0
  answerBatch.partialEvidence = 0
  answerBatch.insufficient = 0
  answerBatch.unavailable = 0
  answerBatch.failed = 0

  try {
    const pageSize = 100
    const selectedState = filters.answer_state || 'all'
    const targetStates: GeneratedQuestionAnswerState[] = selectedState === 'all'
      ? ['not_generated', 'not_found', 'partial_evidence', 'insufficient_source']
      : selectedState === 'answered' ? [] : [selectedState]
    const targets: GeneratedQuestionListItem[] = []

    for (const targetState of targetStates) {
      const generationFilters = { ...filters, answer_state: targetState }
      const firstResponse = await listGeneratedQuestions(props.kbId, {
        ...generationFilters,
        page: 1,
        page_size: pageSize,
      }) as any
      const firstBody = firstResponse?.data && !Array.isArray(firstResponse.data)
        ? firstResponse.data
        : firstResponse
      const targetCount = Number(firstBody?.total || 0)
      if (targetCount + targets.length > 5000) {
        MessagePlugin.error(t('knowledgeEditor.wikiBrowser.generatedQuestionsGenerationLimit'))
        return
      }
      if (Array.isArray(firstBody?.data)) targets.push(...firstBody.data)

      const pageCount = Math.ceil(targetCount / pageSize)
      for (let requestPage = 2; requestPage <= pageCount; requestPage++) {
        const response = await listGeneratedQuestions(props.kbId, {
          ...generationFilters,
          page: requestPage,
          page_size: pageSize,
        }) as any
        const body = response?.data && !Array.isArray(response.data) ? response.data : response
        if (Array.isArray(body?.data)) targets.push(...body.data)
      }
    }

    const targetMap = new Map<string, GeneratedQuestionListItem>()
    targets.forEach((row) => targetMap.set(row.row_key, row))
    const uniqueTargets = [...targetMap.values()]
    answerBatch.total = uniqueTargets.length
    if (uniqueTargets.length === 0) {
      MessagePlugin.info(t('knowledgeEditor.wikiBrowser.generatedQuestionsGenerationNoPending'))
      return
    }

    let nextIndex = 0
    const worker = async () => {
      while (!answerBatch.stopRequested) {
        const index = nextIndex++
        if (index >= uniqueTargets.length) return
        const row = uniqueTargets[index]
        try {
          const response = await extractGeneratedQuestionAnswer(props.kbId, {
            chunk_id: row.chunk_id,
            question_id: row.question_id,
            question_position: row.question_position,
            force_reanalysis: row.answer_state !== 'not_generated',
          }) as any
          const data = response?.data?.data ?? response?.data ?? response
          const status = String(data?.answer_status || 'source_unavailable')
          if (status === 'answered' || status === 'partial_evidence'
            || status === 'insufficient_source' || status === 'not_found') {
            const normalizedStatus = status === 'not_found' ? 'insufficient_source' : status
            if (normalizedStatus === 'answered') answerBatch.answered++
            else if (normalizedStatus === 'partial_evidence') answerBatch.partialEvidence++
            else answerBatch.insufficient++
            row.answer_state = normalizedStatus
            row.answer_created_at = data?.answer_created_at || null
            row.answer_updated_at = data?.answer_updated_at || row.answer_created_at
            row.answer_model_id = String(data?.answer_model_id || '')
            answerCache[row.row_key] = {
              loading: false,
              loaded: true,
              error: false,
              answer: String(data?.answer || ''),
              status: normalizedStatus,
              modelId: row.answer_model_id,
            }
            const visibleRow = rows.value.find((visible) => visible.row_key === row.row_key)
            if (visibleRow) {
              visibleRow.answer_state = row.answer_state
              visibleRow.answer_created_at = row.answer_created_at
              visibleRow.answer_updated_at = row.answer_updated_at
              visibleRow.answer_model_id = row.answer_model_id
            }
          } else {
            answerBatch.unavailable++
          }
        } catch {
          answerBatch.failed++
        } finally {
          answerBatch.done++
        }
      }
    }

    await Promise.all([worker(), worker(), worker()])
    const summary = t('knowledgeEditor.wikiBrowser.generatedQuestionsGenerationSummary', {
      answered: answerBatch.answered,
      partial: answerBatch.partialEvidence,
      insufficient: answerBatch.insufficient,
      unavailable: answerBatch.unavailable,
      failed: answerBatch.failed,
    })
    if (answerBatch.failed > 0) MessagePlugin.error(summary)
    else MessagePlugin.success(summary)
  } catch {
    MessagePlugin.error(t('knowledgeEditor.wikiBrowser.generatedQuestionsGenerationFailed'))
  } finally {
    answerBatch.running = false
    answerBatch.total = Math.max(answerBatch.total, answerBatch.done)
    await loadPage(page.value)
  }
}

async function copyQuestionAndSource(row: GeneratedQuestionListItem) {
  copyingRows[row.row_key] = true
  try {
    await loadSourceOriginal(row)
    const state = sourceCache[row.chunk_id]
    if (!state || state.error) {
      MessagePlugin.error(t('knowledgeEditor.wikiBrowser.generatedQuestionsSourceReadFailed'))
      return
    }
    if (!sourceVersionAvailable(row)) {
      MessagePlugin.error(sourceRevisionLabel(row))
      return
    }
    if (!sourceContentForRow(row).trim()) {
      MessagePlugin.error(t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerUnavailable'))
      return
    }

    await loadQuestionAnswer(row)
    const answer = answerCache[row.row_key]
    if (!answer || answer.error) {
      MessagePlugin.error(t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswerFailed'))
      return
    }

    const sourceContent = sourceContentForRow(row) || t('knowledgeEditor.wikiBrowser.generatedQuestionsSourceEmpty')
    const trace = [
      `document=${row.knowledge_title || row.knowledge_id}`,
      `knowledge_id=${row.knowledge_id}`,
      `chunk_index=${row.chunk_index}`,
      `chunk_id=${row.chunk_id}`,
      `question_id=${row.question_id || '—'}`,
      `question_revision=${row.question_revision ?? 'unknown'}`,
    ].join('; ')
    const text = [
      `${t('knowledgeEditor.wikiBrowser.generatedQuestionsQuestion')}:\n${row.question || t('knowledgeEditor.wikiBrowser.generatedQuestionsMissingText')}`,
      `${t('knowledgeEditor.wikiBrowser.generatedQuestionsAnswer')}:\n${answer.answer || answerEmptyMessage(answer.status)}`,
      `${t('knowledgeEditor.wikiBrowser.generatedQuestionsSource')}:\n${sourceRevisionLabel(row)}\n\n${sourceContent}`,
      `[${trace}]`,
    ].join('\n\n')
    await copyText(text)
  } finally {
    copyingRows[row.row_key] = false
  }
}

async function copyCurrentPage() {
  copying.value = true
  await copyText(rows.value.map(sourceLine).join('\n\n'))
  copying.value = false
}

async function copyFiltered() {
  copyingFiltered.value = true
  try {
    const blob = await exportGeneratedQuestions(props.kbId, { ...filters }, 'text')
    await copyText(await blob.text())
  } catch {
    MessagePlugin.error(t('knowledgeEditor.wikiBrowser.generatedQuestionsCopyFailed'))
  } finally {
    copyingFiltered.value = false
  }
}

async function copyText(value: string) {
  try {
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(value)
        MessagePlugin.success(t('knowledgeEditor.wikiBrowser.generatedQuestionsCopySuccess'))
        return
      } catch {
        // Insecure origins and restricted browsers may expose but reject Clipboard API.
      }
    }

    const textarea = document.createElement('textarea')
    textarea.value = value
    textarea.setAttribute('readonly', '')
    textarea.style.position = 'fixed'
    textarea.style.top = '0'
    textarea.style.left = '-9999px'
    document.body.appendChild(textarea)
    let copied = false
    try {
      textarea.focus()
      textarea.select()
      copied = document.execCommand('copy')
    } finally {
      textarea.remove()
    }
    if (!copied) throw new Error('Clipboard copy was rejected')
    MessagePlugin.success(t('knowledgeEditor.wikiBrowser.generatedQuestionsCopySuccess'))
  } catch {
    MessagePlugin.error(t('knowledgeEditor.wikiBrowser.generatedQuestionsCopyFailed'))
  }
}

async function downloadExport(format: 'csv' | 'json' | 'training_jsonl') {
  exporting.value = true
  try {
    const blob = await exportGeneratedQuestions(props.kbId, { ...filters }, format)
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = format === 'training_jsonl'
      ? 'weknora-retrieval-training.jsonl'
      : `generated-questions.${format}`
    anchor.click()
    URL.revokeObjectURL(url)
  } catch {
    MessagePlugin.error(t('knowledgeEditor.wikiBrowser.generatedQuestionsExportFailed'))
  } finally {
    exporting.value = false
  }
}
</script>

<style scoped>
.generated-questions-drawer {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-height: 100%;
  color: var(--td-text-color-primary);
}

.generated-questions-note {
  padding: 12px 14px;
  border: 1px solid var(--td-border-level-1-color);
  border-radius: 8px;
  background: var(--td-bg-color-container-hover);
  color: var(--td-text-color-secondary);
  line-height: 1.55;
}

.generated-questions-filters {
  display: grid;
  grid-template-columns: repeat(3, minmax(130px, 1fr));
  gap: 8px;
  align-items: center;
}

.generated-questions-actions,
.generated-questions-export-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.generated-questions-actions {
  justify-content: space-between;
}

.generated-questions-count {
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.generated-questions-answer-progress {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border: 1px solid var(--td-brand-color-2);
  border-radius: 6px;
  background: var(--td-brand-color-1);
}

.generated-questions-answer-progress-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.generated-questions-answer-progress progress {
  width: 100%;
  height: 8px;
  accent-color: var(--td-brand-color);
}

.generated-questions-answer-progress-counts,
.answer-generated-at {
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 1.5;
}

.generated-questions-warning,
.generated-questions-error {
  padding: 10px 12px;
  border-radius: 6px;
  line-height: 1.5;
}

.generated-questions-warning {
  background: var(--td-warning-color-1);
  color: var(--td-warning-color-7);
}

.generated-questions-error {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: var(--td-error-color-1);
  color: var(--td-error-color-7);
}

.generated-questions-state {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 160px;
  color: var(--td-text-color-placeholder);
}

.generated-questions-table-wrap {
  overflow: auto;
  border: 1px solid var(--td-border-level-1-color);
  border-radius: 8px;
}

.generated-questions-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.generated-questions-table th,
.generated-questions-table td {
  padding: 12px;
  border-bottom: 1px solid var(--td-border-level-1-color);
  text-align: left;
  vertical-align: top;
}

.generated-questions-table th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  font-weight: 600;
}

.generated-questions-table th:nth-child(1) { width: 34%; }
.generated-questions-table th:nth-child(2) { width: 19%; }
.generated-questions-table th:nth-child(3) { width: 16%; }
.generated-questions-table th:nth-child(4) { width: 16%; }
.generated-questions-table th:nth-child(5) { width: 15%; }

.question-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.55;
}

.question-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 10px;
  margin-top: 8px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  overflow-wrap: anywhere;
}

.copy-question-button {
  margin: 4px 0 0 -8px;
}

.source-original-toggle {
  margin: 4px 0 0 -8px;
}

.source-original-panel {
  margin-top: 8px;
  padding: 10px;
  border: 1px solid var(--td-border-level-1-color);
  border-radius: 6px;
  background: var(--td-bg-color-container-hover);
}

.source-original-heading,
.source-original-error,
.source-original-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.source-original-state {
  justify-content: flex-start;
}

.source-original-error {
  color: var(--td-error-color-7);
}

.source-original-text {
  max-height: 280px;
  margin: 8px 0 0;
  overflow: auto;
  color: var(--td-text-color-primary);
  font: inherit;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.source-answer-panel {
  margin-top: 12px;
  padding: 10px;
  border-radius: 6px;
  background: var(--td-bg-color-container-hover);
}

.source-answer-heading {
  color: var(--td-text-color-primary);
  font-weight: 600;
}

.source-answer-text {
  margin: 6px 0 0;
  color: var(--td-text-color-primary);
  font: inherit;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.source-original-empty {
  margin-top: 8px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.source-title {
  font-weight: 500;
  overflow-wrap: anywhere;
}

.source-id {
  margin-top: 4px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  overflow-wrap: anywhere;
}

.status-cell {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
}

.index-status {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.generated-questions-pagination {
  display: flex;
  justify-content: flex-end;
  padding-bottom: 8px;
}

@media (max-width: 720px) {
  .generated-questions-filters {
    grid-template-columns: 1fr 1fr;
  }

  .generated-questions-table {
    min-width: 820px;
  }
}
</style>
