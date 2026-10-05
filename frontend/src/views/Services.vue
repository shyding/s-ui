<template>
  <ServiceVue 
    v-model="modal.visible"
    :visible="modal.visible"
    :id="modal.id"
    :data="modal.data"
    :inTags="inTags"
    :tsTags="tsTags"
    :ssTags="ssTags"
    :tlsConfigs="tlsConfigs"
    @close="closeModal"
  />
  <v-row>
    <v-col cols="12" justify="center" align="center">
      <v-btn color="primary" @click="showModal(0)">{{ $t('actions.add') }}</v-btn>
    </v-col>
  </v-row>
  <v-row>
    <v-col cols="12">
      <v-row align="center" no-gutters>
        <v-spacer></v-spacer>
        <v-col cols="12" sm="6" md="4" lg="3">
          <v-text-field
            v-model="searchServices"
            :label="$t('search') || 'Search'"
            prepend-inner-icon="mdi-magnify"
            variant="outlined"
            density="compact"
            hide-details
            clearable
          ></v-text-field>
        </v-col>
      </v-row>
      <v-row v-if="selectedServices.length > 0" class="mt-2" no-gutters>
        <v-col cols="auto" class="d-flex align-center ga-2">
          <v-chip color="primary" variant="tonal">已选 {{ selectedServices.length }}</v-chip>
          <v-btn color="error" size="small" variant="outlined" @click="batchDelServiceConfirm = true">
            {{ $t('actions.deleteSelected') || 'Delete Selected' }}
          </v-btn>
          <v-btn size="small" variant="outlined" @click="selectedServices = []">{{ $t('actions.clear') || 'Clear' }}</v-btn>
        </v-col>
      </v-row>
    </v-col>
    <v-col cols="12" sm="4" md="3" lg="2" v-for="(entry, fidx) in filteredServices" :key="entry.item.tag">
      <v-card rounded="xl" elevation="5" min-width="200" :title="entry.item.tag">
        <v-checkbox
          :model-value="selectedServices.includes(entry.item.id)"
          @update:model-value="toggleServiceSelect(entry.item.id, !!$event)"
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
              {{ entry.item.listen }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('in.port') }}</v-col>
            <v-col>
              {{ entry.item.listen_port }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('objects.tls') }}</v-col>
            <v-col>
              {{ entry.item.tls_id > 0 ? $t('enable') : $t('disable') }}
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
                <v-btn color="error" variant="outlined" @click="delSrv(entry.item.id)">{{ $t('yes') }}</v-btn>
                <v-btn color="success" variant="outlined" @click="delOverlay[entry.index] = false">{{ $t('no') }}</v-btn>
              </v-card-actions>
            </v-card>
          </v-overlay>
        </v-card-actions>
      </v-card>
    </v-col>
  </v-row>
  <v-dialog v-model="batchDelServiceConfirm" max-width="400">
    <v-card :title="$t('actions.del')" rounded="lg">
      <v-divider></v-divider>
      <v-card-text>{{ $t('actions.confirmDeleteSelected', { count: selectedServices.length }) || `Delete ${selectedServices.length} selected services?` }}</v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="error" variant="outlined" :loading="batchDeleting" @click="batchDelServices">{{ $t('yes') }}</v-btn>
        <v-btn color="success" variant="outlined" @click="batchDelServiceConfirm = false">{{ $t('no') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script lang="ts" setup>
import Data from '@/store/modules/data'
import { Srv } from '@/types/services'
import { computed, ref } from 'vue'
import ServiceVue from '@/layouts/modals/Service.vue'

const services = computed((): Srv[] => {
  return <Srv[]> Data().services
})

const srvTags = computed((): any[] => {
  return services.value?.map((o:Srv) => o.tag)
})

const tsTags = computed((): any[] => {
  return Data().endpoints?.filter((o:any) => o.type == "tailscale")?.map((o:any) => o.tag)
})

const ssTags = computed((): any[] => {
  return Data().inbounds?.filter((o:any) => o.type == "shadowsocks" && !o.users)?.map((o:any) => o.tag)
})

const inTags = computed((): any[] => {
  return [...Data().inbounds?.map((o:any) => o.tag).filter(t => t != null), ...Data().endpoints?.filter((e:any) => e.listen_port > 0).map((e:any) => e.tag)]
})

const tlsConfigs = computed((): any[] => {
  return <any[]> Data().tlsConfigs
})

const modal = ref({
  visible: false,
  id: 0,
  data: "",
})

let delOverlay = ref(new Array<boolean>)

// Search & batch selection for services
const searchServices = ref('')
const selectedServices = ref<number[]>([])
const batchDelServiceConfirm = ref(false)
const batchDeleting = ref(false)

const filteredServices = computed((): {item: any, index: number}[] => {
  const all = services.value.map((item: any, index: number) => ({ item, index }))
  if (!searchServices.value) return all
  const q = searchServices.value.toLowerCase()
  return all.filter(({ item }) =>
    (item.tag && String(item.tag).toLowerCase().includes(q)) ||
    (item.type && String(item.type).toLowerCase().includes(q))
  )
})

const toggleServiceSelect = (id: number, val: boolean) => {
  if (val) {
    if (!selectedServices.value.includes(id)) selectedServices.value.push(id)
  } else {
    selectedServices.value = selectedServices.value.filter(i => i !== id)
  }
}

const batchDelServices = async () => {
  batchDeleting.value = true
  for (const id of [...selectedServices.value]) {
    const svc = services.value.find((s: any) => s.id === id)
    if (svc) await Data().save("services", "del", svc.tag)
  }
  selectedServices.value = []
  batchDeleting.value = false
  batchDelServiceConfirm.value = false
}

const showModal = (id: number) => {
  modal.value.id = id
  modal.value.data = id == 0 ? '' : JSON.stringify(services.value.findLast(o => o.id == id))
  modal.value.visible = true
}

const closeModal = () => {
  modal.value.visible = false
}

const delSrv = async (id: number) => {
  const index = services.value.findIndex(i => i.id == id)
  const tag = services.value[index].tag

  const success = await Data().save("services", "del", tag)
  if (success) delOverlay.value[index] = false
}
</script>