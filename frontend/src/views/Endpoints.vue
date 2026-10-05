<template>
  <EndpointVue 
    v-model="modal.visible"
    :visible="modal.visible"
    :id="modal.id"
    :data="modal.data"
    :tags="endpointTags"
    @close="closeModal"
  />
  <Stats
    v-model="stats.visible"
    :visible="stats.visible"
    :resource="stats.resource"
    :tag="stats.tag"
    @close="closeStats"
  />
  <QrCode
    v-model="qrcode.visible"
    :visible="qrcode.visible"
    :data="qrcode.data"
    @close="closeQrCode"
  />
  <ProtonSync
    v-model="protonModal.visible"
    :visible="protonModal.visible"
    @close="closeProtonModal"
  />
  <v-row>
    <v-col cols="12" class="d-flex flex-wrap justify-center ga-2">
      <v-btn color="primary" @click="showModal(0)">{{ $t('actions.add') }}</v-btn>
      <v-btn color="deep-purple-accent-3" prepend-icon="mdi-shield-vpn" @click="showProtonModal">ProtonVPN 节点同步</v-btn>
      <v-btn color="amber-darken-3" prepend-icon="mdi-cloud-sync" :loading="cfLoading" @click="refreshCloudflare">刷新 Cloudflare 全球洁净出口</v-btn>
    </v-col>
  </v-row>
  <v-row>
    <v-col cols="12">
      <v-row align="center" no-gutters>
        <v-spacer></v-spacer>
        <v-col cols="12" sm="6" md="4" lg="3">
          <v-text-field
            v-model="searchEndpoints"
            :label="$t('search') || 'Search'"
            prepend-inner-icon="mdi-magnify"
            variant="outlined"
            density="compact"
            hide-details
            clearable
          ></v-text-field>
        </v-col>
      </v-row>
      <v-row v-if="selectedEndpoints.length > 0" class="mt-2" no-gutters>
        <v-col cols="auto" class="d-flex align-center ga-2">
          <v-chip color="primary" variant="tonal">已选 {{ selectedEndpoints.length }}</v-chip>
          <v-btn color="error" size="small" variant="outlined" @click="batchDelEndpointConfirm = true">
            {{ $t('actions.deleteSelected') || 'Delete Selected' }}
          </v-btn>
          <v-btn size="small" variant="outlined" @click="selectedEndpoints = []">{{ $t('actions.clear') || 'Clear' }}</v-btn>
        </v-col>
      </v-row>
    </v-col>
    <v-col cols="12" sm="4" md="3" lg="2" v-for="(entry, fidx) in filteredEndpoints" :key="entry.item.tag">
      <v-card rounded="xl" elevation="5" min-width="200" :title="entry.item.tag">
        <v-checkbox
          :model-value="selectedEndpoints.includes(entry.item.tag)"
          @update:model-value="toggleEndpointSelect(entry.item.tag, !!$event)"
          hide-details
          density="compact"
          class="ml-2 mt-1"
        ></v-checkbox>
        <v-card-subtitle style="margin-top: -20px;">
          <v-row>
            <v-col>{{ entry.item.type }}</v-col>
          </v-row>
        </v-card-subtitle>
        <v-card-text>
          <v-row>
            <v-col>{{ $t('in.addr') }}</v-col>
            <v-col>
              {{ entry.item.address?.length>0 ? entry.item.address[0] : '-' }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('in.port') }}</v-col>
            <v-col>
              {{ entry.item.listen_port>0 ? entry.item.listen_port : '-' }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('types.wg.peers') }}</v-col>
            <v-col>
              {{ entry.item.peers?.length?? '-'  }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('online') }}</v-col>
            <v-col>
              <template v-if="onlines.includes(entry.item.tag)">
                <v-chip density="comfortable" size="small" color="success" variant="flat">{{ $t('online') }}</v-chip>
              </template>
              <template v-else>-</template>
            </v-col>
          </v-row>
        </v-card-text>
        <v-divider></v-divider>
        <v-card-actions style="padding: 0;">
          <v-btn icon="mdi-file-edit" @click="showModal(entry.item.id)">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.edit')"></v-tooltip>
          </v-btn>
          <v-btn icon="mdi-file-remove" style="margin-inline-start:0;" color="warning" @click="delOverlay[entry.index] = true">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.del')"></v-tooltip>
          </v-btn>
          <v-overlay
            v-model="delOverlay[entry.index]"
            contained
            class="align-center justify-center"
          >
            <v-card :title="$t('actions.del')" rounded="lg">
              <v-divider></v-divider>
              <v-card-text>{{ $t('confirm') }}</v-card-text>
              <v-card-actions>
                <v-btn color="error" variant="outlined" @click="delEndpoint(entry.item.tag)">{{ $t('yes') }}</v-btn>
                <v-btn color="success" variant="outlined" @click="delOverlay[entry.index] = false">{{ $t('no') }}</v-btn>
              </v-card-actions>
            </v-card>
          </v-overlay>
          <v-icon
          class="me-2"
          v-if="entry.item.type == 'wireguard' && entry.item.peers?.length>0"
          @click="showQrCode(entry.item.id)"
        >
          mdi-qrcode
        </v-icon>
          <v-btn icon="mdi-chart-line" @click="showStats(entry.item.tag)" v-if="Data().enableTraffic">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('stats.graphTitle')"></v-tooltip>
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-col>
  </v-row>
  <v-dialog v-model="batchDelEndpointConfirm" max-width="400">
    <v-card :title="$t('actions.del')" rounded="lg">
      <v-divider></v-divider>
      <v-card-text>{{ $t('actions.confirmDeleteSelected', { count: selectedEndpoints.length }) || `Delete ${selectedEndpoints.length} selected endpoints?` }}</v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="error" variant="outlined" :loading="batchDeleting" @click="batchDelEndpoints">{{ $t('yes') }}</v-btn>
        <v-btn color="success" variant="outlined" @click="batchDelEndpointConfirm = false">{{ $t('no') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script lang="ts" setup>
import Data from '@/store/modules/data'
import EndpointVue from '@/layouts/modals/Endpoint.vue'
import ProtonSync from '@/layouts/modals/ProtonSync.vue'
import Stats from '@/layouts/modals/Stats.vue'
import QrCode from '@/layouts/modals/WgQrCode.vue'
import { Endpoint } from '@/types/endpoints'
import { computed, ref } from 'vue'

const endpoints = computed((): Endpoint[] => {
  return <Endpoint[]> Data().endpoints
})

const endpointTags = computed((): any[] => {
  return endpoints.value?.map((o:Endpoint) => o.tag)
})

const onlines = computed(() => {
  return [...Data().onlines.inbound?? [], ...Data().onlines.outbound??[] ]
})

const modal = ref({
  visible: false,
  id: 0,
  data: "",
})

const protonModal = ref({
  visible: false,
})

import HttpUtils from '@/plugins/httputil'
import { push } from 'notivue'

const cfLoading = ref(false)

const refreshCloudflare = async () => {
  cfLoading.value = true
  try {
    const res = await HttpUtils.post('api/cloudflareRefresh', {})
    if (res?.success) {
      push.success({
        message: res.obj?.message || `Cloudflare 全球出口已同步 ${res.obj?.regionCount ?? 0} 个国家地区，${res.obj?.endpointCount ?? 0} 个在线出口端点`,
      })
      await Data().loadData()
    }
  } finally {
    cfLoading.value = false
  }
}

const showProtonModal = () => {
  protonModal.value.visible = true
}

const closeProtonModal = () => {
  protonModal.value.visible = false
}

let delOverlay = ref(new Array<boolean>)

// Search & batch selection for endpoints
const searchEndpoints = ref('')
const selectedEndpoints = ref<string[]>([])
const batchDelEndpointConfirm = ref(false)
const batchDeleting = ref(false)

const filteredEndpoints = computed((): {item: Endpoint, index: number}[] => {
  const all = endpoints.value.map((item: Endpoint, index: number) => ({ item, index }))
  if (!searchEndpoints.value) return all
  const q = searchEndpoints.value.toLowerCase()
  return all.filter(({ item }) =>
    (item.tag && String(item.tag).toLowerCase().includes(q)) ||
    (item.type && String(item.type).toLowerCase().includes(q))
  )
})

const toggleEndpointSelect = (tag: string, val: boolean) => {
  if (val) {
    if (!selectedEndpoints.value.includes(tag)) selectedEndpoints.value.push(tag)
  } else {
    selectedEndpoints.value = selectedEndpoints.value.filter(t => t !== tag)
  }
}

const batchDelEndpoints = async () => {
  batchDeleting.value = true
  for (const tag of [...selectedEndpoints.value]) {
    await Data().save("endpoints", "del", tag)
  }
  selectedEndpoints.value = []
  batchDeleting.value = false
  batchDelEndpointConfirm.value = false
}

const showModal = (id: number) => {
  modal.value.id = id
  modal.value.data = id == 0 ? '' : JSON.stringify(endpoints.value.findLast(o => o.id == id))
  modal.value.visible = true
}

const closeModal = () => {
  modal.value.visible = false
}

const stats = ref({
  visible: false,
  resource: "endpoint",
  tag: "",
})

const delEndpoint = async (tag: string) => {
  const index = endpoints.value.findIndex(i => i.tag == tag)
  const success = await Data().save("endpoints", "del", tag)
  if (success) delOverlay.value[index] = false
}

const showStats = (tag: string) => {
  stats.value.tag = tag
  stats.value.visible = true
}
const closeStats = () => {
  stats.value.visible = false
}

const qrcode = ref({
  visible: false,
  data: <any>{},
})

const showQrCode = (id: number) => {
  qrcode.value.data = endpoints.value.findLast(o => o.id == id)
  qrcode.value.visible = true
}
const closeQrCode = () => {
  qrcode.value.visible = false
}
</script>