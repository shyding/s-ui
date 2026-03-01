<template>
  <InboundVue 
    v-model="modal.visible"
    :visible="modal.visible"
    :id="modal.id"
    :inTags="inTags"
    :tlsConfigs="tlsConfigs"
    @close="closeModal"
  />
  <Stats
    v-model="stats.visible"
    :visible="stats.visible"
    :resource="stats.resource"
    :tag="stats.tag"
    @close="closeStats"
  />
  <ClientModal
    v-model="clientModal.visible"
    :visible="clientModal.visible"
    :id="clientModal.id"
    :inboundTags="inboundTags"
    :groups="clientGroups"
    @close="closeClientModal"
  />

  <!-- Copy Dialog -->
  <v-dialog v-model="copyDialog.visible" max-width="400">
    <v-card :title="$t('actions.copy') + ' ' + $t('objects.inbound')" rounded="lg">
      <v-divider></v-divider>
      <v-card-text>
        <p class="mb-2">{{ $t('actions.copy') }}: {{ selectedTags.length }} {{ $t('objects.inbound') }}</p>
        <v-text-field
          v-model.number="copyDialog.count"
          type="number"
          :label="$t('actions.copy_count') || 'Copy Count'"
          min="1"
          max="100"
          density="compact"
          variant="outlined"
          hide-details
        ></v-text-field>
      </v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="primary" variant="outlined" :loading="copyDialog.loading" @click="doCopy">{{ $t('yes') }}</v-btn>
        <v-btn color="error" variant="outlined" @click="copyDialog.visible = false">{{ $t('no') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Delete Confirm Dialog -->
  <v-dialog v-model="deleteConfirmDialog" max-width="400">
    <v-card :title="$t('actions.del')" rounded="lg">
      <v-divider></v-divider>
      <v-card-text>
        {{ $t('actions.confirmDeleteSelected', { count: selectedTags.length }) || `Are you sure you want to delete ${selectedTags.length} selected inbounds?` }}
      </v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="error" variant="outlined" :loading="deleting" @click="deleteSelected">{{ $t('yes') }}</v-btn>
        <v-btn color="success" variant="outlined" @click="deleteConfirmDialog = false">{{ $t('no') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Reassign Users Dialog -->
  <v-dialog v-model="reassignDialog.visible" max-width="500">
    <v-card rounded="lg">
      <v-card-title>{{ $t('in.reassignUsers') || 'Reassign Users' }}</v-card-title>
      <v-divider></v-divider>
      <v-card-text>
        <p class="mb-3 text-caption">{{ reassignDialog.inboundTag }}</p>
        <v-autocomplete
          v-model="reassignDialog.selectedClientIds"
          :items="allClients"
          item-title="name"
          item-value="id"
          :label="$t('pages.clients') || 'Clients'"
          variant="outlined"
          density="compact"
          multiple
          chips
          closable-chips
          clearable
          hide-details
        >
          <template v-slot:item="{ item, props }">
            <v-list-item v-bind="props">
              <template v-slot:prepend="{ isActive }">
                <v-list-item-action start>
                  <v-checkbox-btn :model-value="isActive"></v-checkbox-btn>
                </v-list-item-action>
              </template>
              <template v-slot:subtitle>
                <span v-if="(item.raw as any).group">{{ (item.raw as any).group }}</span>
              </template>
            </v-list-item>
          </template>
        </v-autocomplete>

        <v-divider class="my-3"></v-divider>
        <p class="text-subtitle-2 mb-2">{{ $t('in.byGroup') || 'Quick Select by Group' }}</p>
        <div class="d-flex flex-wrap ga-2">
          <v-chip
            v-for="group in clientGroups"
            :key="group"
            :color="isGroupFullySelected(group) ? 'primary' : 'default'"
            variant="outlined"
            size="small"
            @click="toggleGroup(group)"
          >
            {{ group || $t('none') }}
          </v-chip>
        </div>
      </v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="primary" variant="outlined" :loading="reassignDialog.loading" @click="doReassign">{{ $t('actions.save') }}</v-btn>
        <v-btn color="error" variant="outlined" @click="reassignDialog.visible = false">{{ $t('actions.close') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- User List Dialog -->
  <v-dialog v-model="userListDialog.visible" max-width="500">
    <v-card rounded="lg">
      <v-card-title>{{ $t('pages.clients') }} - {{ userListDialog.inboundTag }}</v-card-title>
      <v-divider></v-divider>
      <v-card-text v-if="userListDialog.clients.length > 0">
        <v-list density="compact">
          <v-list-item v-for="c in userListDialog.clients" :key="c.id" @click="openClientEdit(c.id)" style="cursor: pointer;">
            <template v-slot:prepend>
              <v-icon :color="c.enable ? 'success' : 'grey'" size="small">{{ c.enable ? 'mdi-account-check' : 'mdi-account-off' }}</v-icon>
            </template>
            <v-list-item-title class="text-primary">{{ c.name }}</v-list-item-title>
            <v-list-item-subtitle v-if="c.group">{{ c.group }}</v-list-item-subtitle>
            <template v-slot:append>
              <v-icon size="small" color="grey">mdi-pencil</v-icon>
            </template>
          </v-list-item>
        </v-list>
      </v-card-text>
      <v-card-text v-else>
        <p class="text-center text-grey">{{ $t('noData') }}</p>
      </v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="primary" variant="outlined" @click="userListDialog.visible = false">{{ $t('actions.close') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  <!-- Action Buttons -->
  <v-row>
    <v-col cols="12" justify="center" align="center">
      <v-btn color="primary" @click="showModal(0)">{{ $t('actions.add') }}</v-btn>
      <v-btn
        color="info"
        class="ml-2"
        @click="showCopyDialog"
        :disabled="selectedTags.length === 0"
      >
        {{ $t('actions.copy') }} ({{ selectedTags.length }})
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
      <v-autocomplete
        v-model="filterUser"
        :items="userOptions"
        :label="$t('pages.clients') || 'Client'"
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

  <!-- Inbound Cards -->
  <v-row>
    <v-col cols="12" sm="4" md="3" lg="2" v-for="(item, index) in pagedInbounds" :key="item.tag">
      <v-card rounded="xl" elevation="5" min-width="200" :title="item.tag">
        <template v-slot:append>
            <div class="d-flex align-center">
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
              {{ item.listen }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('in.port') }}</v-col>
            <v-col>
              {{ item.listen_port }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('objects.tls') }}</v-col>
            <v-col>
              {{ item.tls_id > 0 ? $t('enable') : $t('disable') }}
            </v-col>
          </v-row>
          <v-row @click="showUserList(item)" style="cursor: pointer;">
            <v-col>{{ $t('pages.clients') }}</v-col>
            <v-col>
              <template v-if="item.users && item.users.length > 0">
                <a class="text-primary text-decoration-underline">{{ item.users.length }}</a>
              </template>
              <template v-else>-</template>
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('online') }}</v-col>
            <v-col>
              <template v-if="onlines.includes(item.tag)">
                <v-chip density="comfortable" size="small" color="success" variant="flat">{{ $t('online') }}</v-chip>
              </template>
              <template v-else>-</template>
            </v-col>
          </v-row>
        </v-card-text>
        <v-divider></v-divider>
        <v-card-actions style="padding: 0;">
          <v-btn icon="mdi-file-edit" @click="showModal(item.id)">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.edit')"></v-tooltip>
          </v-btn>
          <v-btn icon="mdi-account-multiple" style="margin-inline-start:0;" color="info" @click="showReassignDialog(item)">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('in.reassignUsers') || 'Reassign Users'"></v-tooltip>
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
                <v-btn color="error" variant="outlined" @click="delInbound(item.tag, index)">{{ $t('yes') }}</v-btn>
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

  <!-- Pagination -->
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
import InboundVue from '@/layouts/modals/Inbound.vue'
import Stats from '@/layouts/modals/Stats.vue'
import { Config } from '@/types/config'
import { computed, ref } from 'vue'
import { Inbound } from '@/types/inbounds'
import ClientModal from '@/layouts/modals/Client.vue'
import HttpUtils from '@/plugins/httputil'
import { push } from 'notivue'
import { i18n } from '@/locales'

const appConfig = computed((): Config => {
  return <Config> Data().config
})

const inbounds = computed((): Inbound[] => {
  return <Inbound[]> Data().inbounds
})

const tlsConfigs = computed((): any[] => {
  return <any[]> Data().tlsConfigs
})

const inTags = computed((): string[] => {
  return [...inbounds.value?.map(i => i.tag), ...Data().endpoints?.filter((e:any) => e.listen_port > 0).map((e:any) => e.tag)]
})

const onlines = computed(() => {
  return Data().onlines.inbound?? []
})

// All clients from data store
const allClients = computed(() => {
  return Data().clients?.map((c: any) => ({ id: c.id, name: c.name, group: c.group, inbounds: c.inbounds })) ?? []
})

// Inbound tags for ClientModal
const inboundTags = computed((): any[] => {
  return inbounds.value?.filter((i: any) => i.tag != '' && i.users).map((i: any) => ({ title: i.tag, value: i.id })) ?? []
})

// Unique client names for filter dropdown
const userOptions = computed(() => {
  const names = new Set<string>()
  Data().clients?.forEach((c: any) => {
    if (c.name) names.add(c.name)
  })
  return Array.from(names).sort()
})

// Client groups for quick select
const clientGroups = computed(() => {
  const groups = new Set<string>()
  Data().clients?.forEach((c: any) => {
    groups.add(c.group || '')
  })
  return Array.from(groups).sort()
})

const modal = ref({
  visible: false,
  id: 0,
})

// Search & Pagination
const search = ref('')
const filterUser = ref<string | null>(null)
const page = ref(1)
const pageSize = ref(12)

const filteredInbounds = computed((): Inbound[] => {
  let items = inbounds.value;
  
  // Keyword search
  if (search.value) {
    const q = search.value.toLowerCase();
    items = items.filter(i => 
      i.tag.toLowerCase().includes(q) || 
      i.type.toLowerCase().includes(q) || 
      (i.listen_port && i.listen_port.toString().includes(q))
    );
  }
  
  // Filter by user
  if (filterUser.value) {
    items = items.filter(i => 
      i.users && i.users.includes(filterUser.value!)
    );
  }
  
  return items;
})

const pageCount = computed(() => Math.ceil(filteredInbounds.value.length / pageSize.value))

const pagedInbounds = computed((): Inbound[] => {
  const start = (page.value - 1) * pageSize.value;
  const end = start + pageSize.value;
  return filteredInbounds.value.slice(start, end);
})

// Multi-select
const selectedTags = ref<string[]>([])

// Delete overlay for individual cards
let delOverlay = ref(new Array<boolean>)

const showModal = (id: number) => {
  modal.value.id = id
  modal.value.visible = true
}
const closeModal = () => {
  modal.value.visible = false
}

const delInbound = async (tag: string, index: number) => {
  const success = await Data().save("inbounds", "del", tag)
  if (success) delOverlay.value[index] = false
}

// Copy Dialog
const copyDialog = ref({
  visible: false,
  count: 1,
  loading: false,
})

const showCopyDialog = () => {
  copyDialog.value.count = 1
  copyDialog.value.visible = true
}

const doCopy = async () => {
  copyDialog.value.loading = true
  
  for (const tag of selectedTags.value) {
    const item = inbounds.value.find(i => i.tag === tag)
    if (!item) continue
    await Data().copyInbound(item.id, copyDialog.value.count)
  }
  
  copyDialog.value.loading = false
  copyDialog.value.visible = false
  selectedTags.value = []
}

// Delete selected
const deleteConfirmDialog = ref(false)
const deleting = ref(false)

const deleteSelected = async () => {
  if (selectedTags.value.length === 0) return
  deleting.value = true
  deleteConfirmDialog.value = false
  
  for (const tag of selectedTags.value) {
    await Data().save("inbounds", "del", tag)
  }
  
  selectedTags.value = []
  deleting.value = false
}

// Reassign Users Dialog
const reassignDialog = ref({
  visible: false,
  inboundId: 0,
  inboundTag: '',
  selectedClientIds: <number[]>[],
  loading: false,
})

const showReassignDialog = (item: any) => {
  reassignDialog.value.inboundId = item.id
  reassignDialog.value.inboundTag = item.tag
  
  // Find which clients currently have this inbound
  const currentClientIds = Data().clients
    ?.filter((c: any) => {
      const ids = Array.isArray(c.inbounds) ? c.inbounds : []
      return ids.includes(item.id)
    })
    .map((c: any) => c.id) ?? []
  
  reassignDialog.value.selectedClientIds = [...currentClientIds]
  reassignDialog.value.visible = true
}

const isGroupFullySelected = (group: string) => {
  const groupClients = allClients.value.filter((c: any) => (c.group || '') === group)
  return groupClients.length > 0 && groupClients.every((c: any) => reassignDialog.value.selectedClientIds.includes(c.id))
}

const toggleGroup = (group: string) => {
  const groupClientIds = allClients.value
    .filter((c: any) => (c.group || '') === group)
    .map((c: any) => c.id)
  
  if (isGroupFullySelected(group)) {
    // Remove all from this group
    reassignDialog.value.selectedClientIds = reassignDialog.value.selectedClientIds.filter(
      id => !groupClientIds.includes(id)
    )
  } else {
    // Add all from this group
    const current = new Set(reassignDialog.value.selectedClientIds)
    groupClientIds.forEach((id: number) => current.add(id))
    reassignDialog.value.selectedClientIds = Array.from(current)
  }
}

const doReassign = async () => {
  reassignDialog.value.loading = true
  const inboundId = reassignDialog.value.inboundId
  const clientIds = reassignDialog.value.selectedClientIds.join(',')
  
  const msg = await HttpUtils.post('api/reassignInboundUsers', { inboundId, clientIds })
  
  reassignDialog.value.loading = false
  reassignDialog.value.visible = false
  
  if (msg.success) {
    Data().setNewData(msg.obj)
    push.success({
      title: i18n.global.t('success'),
      duration: 3000,
      message: i18n.global.t('in.reassignUsers') || 'Users reassigned'
    })
  }
}

// User List Dialog
const userListDialog = ref({
  visible: false,
  inboundTag: '',
  clients: <any[]>[],
})

// Client Edit Modal (from user list)
const clientModal = ref({
  visible: false,
  id: 0,
})

const openClientEdit = (clientId: number) => {
  userListDialog.value.visible = false
  clientModal.value.id = clientId
  clientModal.value.visible = true
}

const closeClientModal = () => {
  clientModal.value.visible = false
}

const showUserList = (item: any) => {
  userListDialog.value.inboundTag = item.tag
  // Find clients that have this inbound ID in their inbounds array
  userListDialog.value.clients = Data().clients
    ?.filter((c: any) => {
      const ids = Array.isArray(c.inbounds) ? c.inbounds : []
      return ids.includes(item.id)
    })
    .map((c: any) => ({ id: c.id, name: c.name, group: c.group, enable: c.enable })) ?? []
  userListDialog.value.visible = true
}

// Stats
const stats = ref({
  visible: false,
  resource: "inbound",
  tag: "",
})

const showStats = (tag: string) => {
  stats.value.tag = tag
  stats.value.visible = true
}
const closeStats = () => {
  stats.value.visible = false
}
</script>