<template>
  <v-row>
    <v-col cols="12" class="d-flex flex-wrap justify-center ga-2">
      <v-btn color="primary" @click="showAddModal">{{ $t('actions.add') }}</v-btn>
      <v-btn color="secondary"  @click="showHProxyModal">
        添加 HProxy 实时源
      </v-btn>
      <v-btn color="secondary"  @click="showProxyScrapeModal">
        添加 ProxyScrape 实时源
      </v-btn>
      <v-btn 
        color="success" 
         
        @click="refreshSelected"
        :disabled="selectedIds.length === 0"
        :loading="refreshing"
      >
        {{ $t('subscription.refresh') || 'Refresh Selected' }} ({{ selectedIds.length }})
      </v-btn>
      <v-btn 
        color="info" 
         
        @click="viewSelectedNodes"
        :disabled="selectedIds.length === 0"
      >
        {{ $t('subscription.viewSelectedNodes') || 'View Selected Nodes' }} ({{ selectedIds.length }})
      </v-btn>
      <v-btn 
        color="warning" 
         
        @click="selectedIds = []"
        v-if="selectedIds.length > 0"
      >
        {{ $t('actions.clearSelection') || 'Clear Selection' }}
      </v-btn>
    </v-col>
  </v-row>

  <!-- Add/Edit Modal -->
  <v-dialog v-model="modal.visible" max-width="600">
    <v-card>
      <v-card-title>{{ modal.isEdit ? ($t('actions.edit') || 'Edit') : ($t('actions.add') || 'Add') }} {{ $t('subscription.title') || 'Subscription' }}</v-card-title>
      <v-card-text>
        <v-container>
          <v-row>
            <v-col cols="12">
              <v-text-field
                v-model="modal.name"
                :label="$t('subscription.name') || 'Name'"
                variant="outlined"
                density="compact"
              ></v-text-field>
            </v-col>
          </v-row>
          <v-row>
            <v-col cols="12">
              <v-text-field
                v-model="modal.url"
                :label="$t('subscription.url') || 'Subscription URL'"
                variant="outlined"
                density="compact"
              ></v-text-field>
            </v-col>
          </v-row>
          <v-row>
            <v-col cols="6">
              <v-select
                v-model="modal.updateMode"
                :items="updateModes"
                :label="$t('subscription.updateMode') || 'Update Mode'"
                variant="outlined"
                density="compact"
              ></v-select>
            </v-col>
            <v-col cols="6">
              <v-text-field
                v-model.number="modal.interval"
                :label="$t('subscription.interval') || 'Interval (minutes)'"
                type="number"
                variant="outlined"
                density="compact"
                :hint="$t('subscription.intervalHint') || '0 = manual only'"
              ></v-text-field>
            </v-col>
          </v-row>
          <v-row v-if="modal.isEdit">
            <v-col cols="12">
              <v-switch
                v-model="modal.enabled"
                :label="$t('enable') || 'Enabled'"
                color="primary"
              ></v-switch>
            </v-col>
          </v-row>
        </v-container>
      </v-card-text>
      <v-card-actions>
        <v-btn color="primary" @click="saveSubscription" :loading="saving">{{ $t('actions.save') || 'Save' }}</v-btn>
        <v-spacer></v-spacer>
        <v-btn @click="modal.visible = false">{{ $t('actions.close') || 'Close' }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Search -->
  <v-row class="mt-2">
    <v-col cols="12" sm="6" md="4" lg="3">
      <v-text-field
        v-model="search"
        :label="$t('search') || 'Search'"
        prepend-inner-icon="mdi-magnify"
        variant="outlined"
        density="compact"
        hide-details
        clearable
      ></v-text-field>
    </v-col>
    <v-col cols="12" sm="6" md="8" lg="9" class="d-flex align-center" v-if="selectedIds.length > 0">
      <v-btn color="error" size="small" variant="outlined" @click="batchDeleteConfirm = true" class="mr-2">
        {{ $t('actions.deleteSelected') || 'Delete Selected' }} ({{ selectedIds.length }})
      </v-btn>
      <v-btn color="secondary" size="small" variant="outlined" @click="selectedIds = []">
        {{ $t('actions.clearSelection') || 'Clear' }}
      </v-btn>
    </v-col>
  </v-row>

  <!-- Subscriptions List -->
  <v-row class="mt-4">
    <v-col cols="12" md="6" lg="4" v-for="sub in filteredSubscriptions" :key="sub.id">
      <v-card rounded="xl" elevation="5">
        <template v-slot:prepend>
          <v-checkbox
            v-model="selectedIds"
            :value="sub.id"
            hide-details
            density="compact"
          ></v-checkbox>
        </template>
        <template v-slot:title>
          {{ sub.name }}
        </template>
        <template v-slot:append>
          <v-chip :color="sub.enabled ? 'success' : 'grey'" size="small">
            {{ sub.enabled ? ($t('enable') || 'Enabled') : ($t('disable') || 'Disabled') }}
          </v-chip>
        </template>
        <v-card-text>
          <div class="text-caption text-truncate mb-1">{{ sub.url }}</div>
          <div class="d-flex justify-space-between">
            <span><v-icon size="small">mdi-account-multiple</v-icon> {{ sub.nodeCount || 0 }} {{ $t('subscription.nodes') || 'nodes' }}</span>
            <span><v-icon size="small">mdi-clock</v-icon> {{ formatInterval(sub.updateInterval) }}</span>
          </div>
          <div class="text-caption text-grey mt-1" v-if="sub.lastUpdate">
            {{ $t('subscription.lastUpdate') || 'Last update' }}: {{ formatDate(sub.lastUpdate) }}
          </div>
        </v-card-text>
        <v-card-actions>
          <v-btn size="small" icon @click="refreshSingle(sub.id)" :loading="refreshingId === sub.id">
            <v-icon>mdi-refresh</v-icon>
            <v-tooltip activator="parent">{{ $t('subscription.refresh') || 'Refresh' }}</v-tooltip>
          </v-btn>
          <v-btn size="small" icon @click="editSubscription(sub)">
            <v-icon>mdi-pencil</v-icon>
            <v-tooltip activator="parent">{{ $t('actions.edit') || 'Edit' }}</v-tooltip>
          </v-btn>
          <v-btn size="small" icon @click="viewNodes(sub)">
            <v-icon>mdi-view-list</v-icon>
            <v-tooltip activator="parent">{{ $t('subscription.viewNodes') || 'View Nodes' }}</v-tooltip>
          </v-btn>
          <v-spacer></v-spacer>
          <v-btn size="small" icon color="error" @click="confirmDelete(sub)">
            <v-icon>mdi-delete</v-icon>
            <v-tooltip activator="parent">{{ $t('actions.del') || 'Delete' }}</v-tooltip>
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-col>
  </v-row>

  <!-- Delete Confirm Dialog -->
  <v-dialog v-model="batchDeleteConfirm" max-width="400">
    <v-card :title="$t('actions.del')" rounded="lg">
      <v-divider></v-divider>
      <v-card-text>{{ $t('actions.confirmDeleteSelected', { count: selectedIds.length }) || `Delete ${selectedIds.length} selected subscriptions?` }}</v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="error" variant="outlined" :loading="deleting" @click="batchDelete">{{ $t('yes') }}</v-btn>
        <v-btn color="success" variant="outlined" @click="batchDeleteConfirm = false">{{ $t('no') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  <v-dialog v-model="deleteDialog.visible" max-width="400">
    <v-card>
      <v-card-title>{{ $t('actions.del') || 'Delete' }}</v-card-title>
      <v-card-text>
        {{ $t('subscription.confirmDelete') || 'Are you sure you want to delete this subscription? All imported nodes will also be deleted.' }}
      </v-card-text>
      <v-card-actions>
        <v-btn color="error" @click="doDelete" :loading="deleting">{{ $t('yes') || 'Yes' }}</v-btn>
        <v-spacer></v-spacer>
        <v-btn @click="deleteDialog.visible = false">{{ $t('no') || 'No' }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>


  <!-- Node Test Modal -->
  <NodeTest
    v-model="nodeTest.visible"
    :tags="nodeTest.tags"
    :initialNodes="nodeTest.nodes"
    @close="nodeTest.visible = false"
  ></NodeTest>
</template>

<script lang="ts" setup>
import { ref, onMounted, computed } from 'vue'
import HttpUtils from '@/plugins/httputil'
import NodeTest from '@/layouts/modals/NodeTest.vue'

interface Subscription {
  id: number
  name: string
  url: string
  enabled: boolean
  updateMode: string
  updateInterval: number
  lastUpdate: number
  nodeCount: number
}

const subscriptions = ref<Subscription[]>([])
const selectedIds = ref<number[]>([])
const search = ref('')
const batchDeleteConfirm = ref(false)

const filteredSubscriptions = computed(() => {
  if (!search.value) return subscriptions.value
  const q = search.value.toLowerCase()
  return subscriptions.value.filter(s =>
    (s.name && s.name.toLowerCase().includes(q)) ||
    (s.url && s.url.toLowerCase().includes(q))
  )
})

const batchDelete = async () => {
  deleting.value = true
  try {
    for (const id of selectedIds.value) {
      await HttpUtils.post('api/deleteSubscription', { id: id.toString() })
    }
    selectedIds.value = []
    batchDeleteConfirm.value = false
    await loadSubscriptions()
  } finally {
    deleting.value = false
  }
}
const refreshing = ref(false)
const refreshingId = ref<number | null>(null)
const saving = ref(false)
const deleting = ref(false)

const modal = ref({
  visible: false,
  isEdit: false,
  id: 0,
  name: '',
  url: '',
  updateMode: 'replace',
  interval: 0,
  enabled: true
})

const deleteDialog = ref({
  visible: false,
  id: 0
})

const nodeTest = ref({
  visible: false,
  tags: [] as string[],
  nodes: [] as any[]
})

const updateModes = [
  { title: 'Replace All', value: 'replace' },
  { title: 'Incremental', value: 'incremental' }
]

const loadSubscriptions = async () => {
  const response = await HttpUtils.get('api/subscriptions')
  if (response.success && response.obj) {
    subscriptions.value = response.obj
  }
}

const showAddModal = () => {
  modal.value = {
    visible: true,
    isEdit: false,
    id: 0,
    name: '',
    url: '',
    updateMode: 'replace',
    interval: 0,
    enabled: true
  }
}

const showHProxyModal = () => {
  modal.value = {
    visible: true,
    isEdit: false,
    id: 0,
    name: 'HProxy Live Candidates',
    url: 'https://raw.githubusercontent.com/hproxy-com/free-proxy-list/main/live.json',
    updateMode: 'replace',
    interval: 30,
    enabled: true
  }
}

const showProxyScrapeModal = () => {
  modal.value = {
    visible: true,
    isEdit: false,
    id: 0,
    name: 'ProxyScrape Live Candidates',
    url: 'https://cdn.jsdelivr.net/gh/proxyscrape/free-proxy-list@main/proxies/all/data.json',
    updateMode: 'replace',
    interval: 30,
    enabled: true
  }
}

const editSubscription = (sub: Subscription) => {
  modal.value = {
    visible: true,
    isEdit: true,
    id: sub.id,
    name: sub.name,
    url: sub.url,
    updateMode: sub.updateMode,
    interval: sub.updateInterval,
    enabled: sub.enabled
  }
}

const saveSubscription = async () => {
  saving.value = true
  try {
    const endpoint = modal.value.isEdit ? 'api/updateSubscription' : 'api/addSubscription'
    const params: any = {
      name: modal.value.name,
      url: modal.value.url,
      updateMode: modal.value.updateMode,
      interval: modal.value.interval.toString()
    }
    
    if (modal.value.isEdit) {
      params.id = modal.value.id.toString()
      params.enabled = modal.value.enabled.toString()
    }
    
    const response = await HttpUtils.post(endpoint, params)
    if (response.success) {
      modal.value.visible = false
      await loadSubscriptions()
    }
  } finally {
    saving.value = false
  }
}

const confirmDelete = (sub: Subscription) => {
  deleteDialog.value = {
    visible: true,
    id: sub.id
  }
}

const doDelete = async () => {
  deleting.value = true
  try {
    const response = await HttpUtils.post('api/deleteSubscription', {
      id: deleteDialog.value.id.toString()
    })
    if (response.success) {
      deleteDialog.value.visible = false
      await loadSubscriptions()
    }
  } finally {
    deleting.value = false
  }
}

const refreshSingle = async (id: number) => {
  refreshingId.value = id
  try {
    await HttpUtils.post('api/refreshSubscription', { ids: id.toString() })
    await loadSubscriptions()
  } finally {
    refreshingId.value = null
  }
}

const refreshSelected = async () => {
  if (selectedIds.value.length === 0) return
  
  refreshing.value = true
  try {
    await HttpUtils.post('api/refreshSubscription', {
      ids: selectedIds.value.join(',')
    })
    await loadSubscriptions()
    selectedIds.value = []
  } finally {
    refreshing.value = false
  }
}

const formatInterval = (minutes: number): string => {
  if (minutes === 0) return 'Manual'
  if (minutes < 60) return `${minutes}m`
  return `${Math.floor(minutes / 60)}h`
}

const formatDate = (timestamp: number): string => {
  if (!timestamp) return 'Never'
  return new Date(timestamp * 1000).toLocaleString()
}

onMounted(() => {
  loadSubscriptions()
})

const viewNodes = async (sub: Subscription) => {
  // Clear previous data
  nodeTest.value.nodes = []
  nodeTest.value.tags = []
  
  try {
    const response = await HttpUtils.get('api/subscriptionNodes', { id: sub.id.toString() })
    if (response.success && response.obj) {
      nodeTest.value.nodes = response.obj
      nodeTest.value.tags = response.obj.map((n: any) => n.tag)
      nodeTest.value.visible = true
    }
  } catch (error) {
    console.error('Failed to load nodes:', error)
  }
}

const viewSelectedNodes = async () => {
  if (selectedIds.value.length === 0) return

  // Clear previous data
  nodeTest.value.nodes = []
  nodeTest.value.tags = []
  
  try {
    const ids = selectedIds.value.join(',')
    const response = await HttpUtils.get('api/subscriptionNodes', { id: ids })
    if (response.success && response.obj) {
      nodeTest.value.nodes = response.obj
      nodeTest.value.tags = response.obj.map((n: any) => n.tag)
      nodeTest.value.visible = true
    }
  } catch (error) {
    console.error('Failed to load selected nodes:', error)
  }
}
</script>
