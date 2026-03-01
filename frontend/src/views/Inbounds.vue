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
          <v-row>
            <v-col>{{ $t('pages.clients') }}</v-col>
            <v-col>
              <template v-if="item.users">
                <v-tooltip activator="parent" dir="ltr" location="bottom" v-if="item.users.length > 0">
                  <span v-for="u in item.users">{{ u }}<br /></span>
                </v-tooltip>
                {{ item.users.length }}
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

const modal = ref({
  visible: false,
  id: 0,
})

// Search & Pagination
const search = ref('')
const page = ref(1)
const pageSize = ref(12)

const filteredInbounds = computed((): Inbound[] => {
  if (!search.value) return inbounds.value;
  const q = search.value.toLowerCase();
  return inbounds.value.filter(i => 
    i.tag.toLowerCase().includes(q) || 
    i.type.toLowerCase().includes(q) || 
    (i.listen_port && i.listen_port.toString().includes(q))
  );
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
  
  // Copy each selected inbound
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