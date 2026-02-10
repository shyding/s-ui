<template>
  <OutboundVue 
    v-model="modal.visible"
    :visible="modal.visible"
    :id="modal.id"
    :data="modal.data"
    :tags="outboundTags"
    @close="closeModal"
  />
  <BatchImport
    v-model="batchModal.visible"
    :visible="batchModal.visible"
    @close="closeBatchModal"
  />
  <NodeTest
    v-model="testModal.visible"
    :visible="testModal.visible"
    :tags="tagsForTest"
    @close="closeTestModal"
    @update-results="onTestResults"
  />
  <Stats
    v-model="stats.visible"
    :visible="stats.visible"
    :resource="stats.resource"
    :tag="stats.tag"
    @close="closeStats"
  />
  <v-row>
    <v-col cols="12" justify="center" align="center">
      <v-btn color="primary" @click="showModal(0)">{{ $t('actions.add') }}</v-btn>
      <v-btn color="secondary" class="ml-2" @click="showBatchModal">{{ $t('actions.batchImport') || 'Batch Import' }}</v-btn>
      <v-btn color="info" class="ml-2" @click="showTestModal(false)">{{ $t('actions.testAll') || 'Test All' }}</v-btn>
      <v-btn 
        color="success" 
        class="ml-2" 
        @click="showTestModal(true)"
        :disabled="selectedTags.length === 0"
      >
        {{ $t('nodeTest.testSelected') || 'Test Selected' }} ({{ selectedTags.length }})
      </v-btn>
      <v-btn 
        color="warning" 
        class="ml-2" 
        @click="exportSelected"
        :disabled="selectedTags.length === 0"
        :loading="exporting"
      >
        {{ $t('actions.exportSelected') || 'Export Selected' }} ({{ selectedTags.length }})
      </v-btn>
      <v-btn 
        color="error" 
        class="ml-2" 
        @click="deleteConfirmDialog = true"
        :disabled="selectedTags.length === 0"
        :loading="deleting"
      >
        {{ $t('actions.deleteSelected') || 'Delete Selected' }} ({{ selectedTags.length }})
      </v-btn>
      <v-btn 
        color="secondary" 
        class="ml-2" 
        @click="selectedTags = []"
        :disabled="selectedTags.length === 0"
      >
        {{ $t('actions.clearSelection') || 'Clear Selection' }}
      </v-btn>
      <v-menu>
        <template v-slot:activator="{ props }">
          <v-btn color="primary" class="ml-2" v-bind="props">
            {{ $t('actions.sort') || 'Sort' }}
            <v-icon end>mdi-menu-down</v-icon>
          </v-btn>
        </template>
        <v-list>
          <v-list-item @click="sortBy = 'tag'">
            <v-list-item-title>Tag (A-Z)</v-list-item-title>
          </v-list-item>
          <v-list-item @click="sortBy = 'latency'">
            <v-list-item-title>Latency (Low to High)</v-list-item-title>
          </v-list-item>
          <v-list-item @click="sortBy = 'latency-desc'">
            <v-list-item-title>Latency (High to Low)</v-list-item-title>
          </v-list-item>
        </v-list>
      </v-menu>
    </v-col>
  </v-row>

  <!-- Search & Filter -->
  <v-row>
    <v-col cols="12" sm="6" md="3">
      <v-text-field
        v-model="search"
        :label="$t('search') || 'Search'"
        prepend-inner-icon="mdi-magnify"
        variant="outlined"
        density="compact"
        hide-details
        clearable
        @update:model-value="page = 1"
      ></v-text-field>
    </v-col>
    <v-col cols="6" sm="3" md="2">
      <v-select
        v-model="filterStatus"
        :items="statusOptions"
        :label="$t('nodeTest.status') || 'Status'"
        variant="outlined"
        density="compact"
        hide-details
        clearable
        item-title="title"
        item-value="value"
        @update:model-value="page = 1"
      ></v-select>
    </v-col>
    <v-col cols="6" sm="3" md="2">
      <v-select
        v-model="filterIpType"
        :items="ipTypeOptions"
        :label="$t('nodeTest.type') || 'Type'"
        variant="outlined"
        density="compact"
        hide-details
        clearable
        @update:model-value="page = 1"
      ></v-select>
    </v-col>
    <v-col cols="6" sm="3" md="2">
      <v-autocomplete
        v-model="filterCountry"
        :items="countryOptions"
        :label="$t('nodeTest.location') || 'Location'"
        variant="outlined"
        density="compact"
        hide-details
        clearable
        @update:model-value="page = 1"
      ></v-autocomplete>
    </v-col>
    <v-col cols="6" sm="3" md="2">
       <v-select
          v-model="pageSize"
          :items="[12, 24, 60, 100]"
          :label="$t('perPage') || 'Per Page'"
          variant="outlined"
          density="compact"
          hide-details
       ></v-select>
    </v-col>
  </v-row>

  <!-- Node List -->
  <v-row>
    <v-col cols="12" sm="6" md="4" lg="3" xl="2" v-for="(item, index) in pagedOutbounds" :key="item.tag">
      <v-card rounded="xl" elevation="5" min-width="200" :title="item.tag">
        <template v-slot:append>
            <div class="d-flex align-center">
              <v-chip v-if="getLatency(item.tag) >= 0" :color="getLatencyColor(getLatency(item.tag))" size="small" class="mr-2">
                {{ getLatency(item.tag) }}ms
              </v-chip>
              <v-btn size="small" variant="text" icon density="compact" @click.stop="testNode(item.tag)" :loading="testingTag === item.tag">
                <v-icon size="small">mdi-flash</v-icon>
              </v-btn>
              <v-checkbox
                  v-model="selectedTags"
                  :value="item.tag"
                  hide-details
                  density="compact"
                  color="primary"
              ></v-checkbox>
            </div>
        </template>
        <v-card-subtitle style="margin-top: -20px;">
          <v-row>
            <v-col>{{ item.type }}</v-col>
          </v-row>
        </v-card-subtitle>
        <v-card-text>
          <v-row>
            <v-col>{{ $t('in.addr') }}</v-col>
            <v-col>
              {{ item.server?? '-' }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('in.port') }}</v-col>
            <v-col>
              {{ item.server_port?? '-' }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('objects.tls') }}</v-col>
            <v-col>
              {{ Object.hasOwn(item,'tls') ? $t(item.tls?.enabled ? 'enable' : 'disable') : '-'  }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('nodeTest.status') || 'Status' }}</v-col>
            <v-col>
              <template v-if="onlines.includes(item.tag)">
                <v-chip density="comfortable" size="small" color="success" variant="flat">{{ $t('online') }}</v-chip>
              </template>
              <template v-else-if="item.lastTestTime > 0">
                <v-chip density="comfortable" size="small" :color="item.available ? 'info' : 'error'" variant="flat">
                  {{ item.available ? ($t('nodeTest.available') || 'Available') : ($t('nodeTest.timeout') || 'Time out') }}
                </v-chip>
              </template>
              <template v-else>
                <v-chip density="comfortable" size="small" color="grey" variant="flat" label>
                  {{ $t('nodeTest.untested') || 'Untested' }}
                </v-chip>
              </template>
            </v-col>
          </v-row>
          <v-row v-if="item.country || item.region">
            <v-col>{{ $t('nodeTest.location') || 'Location' }}</v-col>
            <v-col>
              <span class="text-caption">{{ [item.country, item.region, item.city].filter(Boolean).join(' / ') || '-' }}</span>
            </v-col>
          </v-row>
          <v-row v-if="item.ipType || item.fraudScore !== undefined || item.available">
            <v-col>{{ $t('nodeTest.quality') || 'Quality' }}</v-col>
            <v-col>
              <div class="d-flex align-center">
                 <v-chip v-if="item.ipType" size="x-small" :color="item.ipType === 'ISP' ? 'success' : 'warning'" class="mr-1" label>{{ item.ipType }}</v-chip>
                 <v-chip v-else size="x-small" color="grey" class="mr-1" label>Unknown</v-chip>
                 <v-chip v-if="item.fraudScore !== undefined" size="x-small" :color="item.fraudScore < 30 ? 'success' : (item.fraudScore < 70 ? 'warning' : 'error')" label>{{ $t('nodeTest.score') || 'Score' }}: {{ item.fraudScore }}</v-chip>
              </div>
            </v-col>
          </v-row>
        </v-card-text>
        <v-divider></v-divider>
        <v-card-actions style="padding: 0;">
          <v-btn icon="mdi-file-edit" @click="showModal(item.id)">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.edit')"></v-tooltip>
          </v-btn>
          <v-btn icon="mdi-content-copy" style="margin-inline-start:0;" color="primary" @click="copyNodeLink(item.tag)" :loading="copyingTag === item.tag">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.copyLink') || 'Copy Link'"></v-tooltip>
          </v-btn>
          <v-btn icon="mdi-file-remove" style="margin-inline-start:0;" color="warning" @click="delOverlay[index] = true">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.del')"></v-tooltip>
          </v-btn>
          <v-overlay
            v-model="delOverlay[index]"
            contained
            class="align-center justify-center"
          >
            <v-card :title="$t('actions.del')" rounded="lg">
              <v-divider></v-divider>
              <v-card-text>{{ $t('confirm') }}</v-card-text>
              <v-card-actions>
                <v-btn color="error" variant="outlined" @click="delOutbound(item.tag)">{{ $t('yes') }}</v-btn>
                <v-btn color="success" variant="outlined" @click="delOverlay[index] = false">{{ $t('no') }}</v-btn>
              </v-card-actions>
            </v-card>
          </v-overlay>
          <v-btn icon="mdi-chart-line" @click="showStats(item.tag)" v-if="Data().enableTraffic">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('stats.graphTitle')"></v-tooltip>
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-col>
  </v-row>
  
  <v-row v-if="pageCount > 1">
      <v-col cols="12">
          <v-pagination
              v-model="page"
              :length="pageCount"
              :total-visible="7"
          ></v-pagination>
      </v-col>
  </v-row>
</template>

<script lang="ts" setup>
import Data from '@/store/modules/data'
import OutboundVue from '@/layouts/modals/Outbound.vue'
import BatchImport from '@/layouts/modals/BatchImport.vue'
import NodeTest from '@/layouts/modals/NodeTest.vue'
import Stats from '@/layouts/modals/Stats.vue'
import { Outbound } from '@/types/outbounds'
import { computed, ref, reactive } from 'vue'
import HttpUtils from '@/plugins/httputil'

const outbounds = computed((): Outbound[] => {
  return <Outbound[]> Data().outbounds
})

const outboundTags = computed((): string[] => {
  return [...Data().outbounds?.map((o:Outbound) => o.tag), ...Data().endpoints?.map((e:any) => e.tag)]
})

const onlines = computed(() => {
  return Data().onlines.outbound?? []
})

const modal = ref({
  visible: false,
  id: 0,
  data: "",
})

const batchModal = ref({
  visible: false,
})

const testModal = ref({
  visible: false,
})

const selectedTags = ref<string[]>([])

let delOverlay = ref(new Array<boolean>)

const showModal = (id: number) => {
  modal.value.id = id
  modal.value.data = id == 0 ? '' : JSON.stringify(outbounds.value.findLast(o => o.id == id))
  modal.value.visible = true
}

const closeModal = () => {
  modal.value.visible = false
}

// Latency & Sorting
const sortBy = ref('tag')
const latencyMap = reactive(new Map<string, number>())
const testingTag = ref<string | null>(null)

const getLatency = (tag: string) => latencyMap.get(tag) || -1

const getLatencyColor = (latency: number) => {
  if (latency < 100) return 'success'
  if (latency < 300) return 'warning'
  return 'error'
}

// Search & Pagination
const search = ref('')
const page = ref(1)
const pageSize = ref(24)

const filterStatus = ref<string | null>(null)
const filterIpType = ref<string | null>(null)
const filterCountry = ref<string | null>(null)

const ipTypeOptions = computed(() => {
  const types = new Set<string>()
  let hasUnknown = false
  Data().outbounds.forEach((o: Outbound) => {
    if (o.ipType) {
      types.add(o.ipType)
    } else {
      hasUnknown = true
    }
  })
  const arr = Array.from(types).sort()
  if (hasUnknown) arr.push('Unknown')
  return arr
})

const countryOptions = computed(() => {
  const countries = new Set<string>()
  Data().outbounds.forEach((o: Outbound) => {
    if (o.country) countries.add(o.country)
  })
  return Array.from(countries).sort()
})

const statusOptions = computed(() => [
  { title: 'Online', value: 'Online' },
  { title: 'Available', value: 'Available' },
  { title: 'Timeout', value: 'Timeout' },
  { title: 'Untested', value: 'Untested' }
])

const filteredOutbounds = computed(() => {
  let items = [...outbounds.value]
  
  // Keyword Search
  if (search.value) {
    const q = search.value.toLowerCase()
    items = items.filter(o => 
      o.tag.toLowerCase().includes(q) || 
      o.type.toLowerCase().includes(q) || 
      (o.server && o.server.toLowerCase().includes(q)) ||
      (o.landingIP && o.landingIP.toLowerCase().includes(q))
    )
  }

  // Filter by Status
  if (filterStatus.value) {
    items = items.filter(o => {
      const isOnline = onlines.value.includes(o.tag)
      if (filterStatus.value === 'Online') return isOnline
      if (filterStatus.value === 'Available') return !isOnline && o.lastTestTime > 0 && o.available
      if (filterStatus.value === 'Timeout') return !isOnline && o.lastTestTime > 0 && !o.available
      if (filterStatus.value === 'Untested') return !isOnline && (!o.lastTestTime || o.lastTestTime <= 0)
      return true
    })
  }

  // Filter by IP Type
  if (filterIpType.value) {
    if (filterIpType.value === 'Unknown') {
      items = items.filter(o => !o.ipType)
    } else {
      items = items.filter(o => o.ipType === filterIpType.value)
    }
  }

  // Filter by Country
  if (filterCountry.value) {
    items = items.filter(o => o.country === filterCountry.value)
  }

  // Sort
  switch (sortBy.value) {
    case 'latency':
      return items.sort((a, b) => {
        const latA = latencyMap.get(a.tag) || 99999
        const latB = latencyMap.get(b.tag) || 99999
        if (latA === -1 && latB !== -1) return 1
        if (latA !== -1 && latB === -1) return -1
        return latA - latB
      })
    case 'latency-desc':
      return items.sort((a, b) => {
        const latA = latencyMap.get(a.tag) || -1
        const latB = latencyMap.get(b.tag) || -1
        return latB - latA
      })
    case 'tag':
    default:
      return items.sort((a, b) => a.tag.localeCompare(b.tag))
  }
})

const pageCount = computed(() => Math.ceil(filteredOutbounds.value.length / pageSize.value))

const pagedOutbounds = computed(() => {
  const start = (page.value - 1) * pageSize.value
  const end = start + pageSize.value
  return filteredOutbounds.value.slice(start, end)
})

const sortedOutbounds = computed(() => filteredOutbounds.value) // Keep backward comp if needed, or just remove usage

const testNode = async (tag: string) => {
  testingTag.value = tag
  latencyMap.set(tag, -1) // Reset latency display to ensure user sees it's refreshing if valid
  try {
    const response = await HttpUtils.post('api/testSelectedNodes', { tags: tag })
    if (response.success && response.obj && response.obj.length > 0) {
      const result = response.obj[0]
      if (result.available || result.latency >= 0) {
        latencyMap.set(tag, result.latency)
      } else {
        // If failed or timeout
        latencyMap.set(tag, -1) 
      }
    }
  } catch (error) {
    console.error(`Failed to test node ${tag}:`, error)
  } finally {
    testingTag.value = null
  }
}

// Copy node link functionality
const copyingTag = ref<string | null>(null)

const copyNodeLink = async (tag: string) => {
  copyingTag.value = tag
  try {
    const response = await HttpUtils.post('api/exportOutbounds', { tags: tag })
    if (response.success && response.obj && response.obj.length > 0) {
      const link = response.obj[0]
      await navigator.clipboard.writeText(link)
      // Optional: show success toast
    }
  } catch (error) {
    console.error(`Failed to copy link for ${tag}:`, error)
  } finally {
    copyingTag.value = null
  }
}

const onTestResults = (results: any[]) => {
  results.forEach(r => {
    if (r.available || r.latency >= 0) {
      latencyMap.set(r.tag, r.latency)
    } else {
      latencyMap.set(r.tag, -1)
    }
  })
  
  // Reload data to update persistent status (Available, lastTestTime)
  Data().loadData()
}

const showBatchModal = () => {
  batchModal.value.visible = true
}

const closeBatchModal = () => {
  batchModal.value.visible = false
}

const tagsForTest = ref<string[]>([])

const showTestModal = (useSelection: boolean) => {
  if (useSelection) {
    tagsForTest.value = [...selectedTags.value]
  } else {
    tagsForTest.value = []
  }
  testModal.value.visible = true
}

const closeTestModal = () => {
  testModal.value.visible = false
}

const stats = ref({
  visible: false,
  resource: "outbound",
  tag: "",
})

const delOutbound = async (tag: string) => {
  const index = outbounds.value.findIndex(i => i.tag == tag)
  const success = await Data().save("outbounds", "del", tag)
  if (success) delOverlay.value[index] = false
}

const showStats = (tag: string) => {
  stats.value.tag = tag
  stats.value.visible = true
}
const closeStats = () => {
  stats.value.visible = false
}

// Export functionality
const exporting = ref(false)
const exportModal = ref({
  visible: false,
  links: ''
})

const exportSelected = async () => {
  if (selectedTags.value.length === 0) return
  
  exporting.value = true
  try {
    const response = await HttpUtils.post('api/exportOutbounds', {
      tags: selectedTags.value.join(',')
    })
    
    if (response.success && response.obj) {
      exportModal.value.links = response.obj.join('\n')
      exportModal.value.visible = true
    }
  } catch (e) {
    console.error('Export failed:', e)
  } finally {
    exporting.value = false
  }
}

const copyLinks = async () => {
  try {
    await navigator.clipboard.writeText(exportModal.value.links)
  } catch (e) {
    console.error('Copy failed:', e)
  }
}

const downloadLinks = () => {
  const blob = new Blob([exportModal.value.links], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `outbounds_${new Date().toISOString().slice(0,10)}.txt`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

// Delete selected functionality
const deleteConfirmDialog = ref(false)
const deleting = ref(false)

const deleteSelected = async () => {
  if (selectedTags.value.length === 0) return
  
  deleting.value = true
  deleteConfirmDialog.value = false
  try {
    const response = await HttpUtils.post('api/batchDelete', {
      tags: selectedTags.value.join(',')
    })
    
    if (response.success) {
      // Refresh outbounds list
      await Data().loadData()
      // Clear selection
      selectedTags.value = []
    }
  } catch (e) {
    console.error('Delete failed:', e)
  } finally {
    deleting.value = false
  }
}
</script>