<template>
  <DnsVue
    v-model="dnsModal.visible"
    :visible="dnsModal.visible"
    :index="dnsModal.index"
    :data="dnsModal.data"
    :tsTags="tsTags"
    :rslvdTags="rslvdTags"
    @close="closeDnsModal"
    @save="saveDnsModal"
  />
  <DnsRuleVue
    v-model="dnsRuleModal.visible"
    :visible="dnsRuleModal.visible"
    :index="dnsRuleModal.index"
    :data="dnsRuleModal.data"
    :clients="clients"
    :inTags="inboundTags"
    :serverTags="dnsServerTags"
    :ruleSets="ruleSets"
    @close="closeDnsRuleModal"
    @save="saveDnsRuleModal"
  />
  <v-row>
    <v-col cols="12" justify="center" align="center">
      <v-btn color="primary" @click="showDnsModal(-1)" style="margin: 0 5px;">{{ $t('dns.add') }}</v-btn>
      <v-btn color="primary" @click="showDnsRuleModal(-1)" style="margin: 0 5px;">{{ $t('dns.rule.add') }}</v-btn>
      <v-btn variant="outlined" color="warning" @click="saveConfig" :loading="loading" :disabled="stateChange">
        {{ $t('actions.save') }}
      </v-btn>
    </v-col>
  </v-row>
  <v-row>
    <v-col class="v-card-subtitle" cols="12">{{ $t('pages.basics') }}</v-col>
    <v-col cols="12">
      <v-row>
        <v-col cols="12" sm="6" md="3" lg="2">
          <v-select
            hide-details
            :label="$t('dns.final')"
            :items="[ {title: $t('dns.firstServer'), value: ''}, ...dnsServerTags]"
            v-model="finalDns">
          </v-select>
        </v-col>
        <v-col cols="12" sm="6" md="3" lg="2">
          <v-select
            hide-details
            :label="$t('dns.domainStrategy')"
            clearable
            @click:clear="delete dns.strategy"
            :items="['prefer_ipv4','prefer_ipv6','ipv4_only','ipv6_only']"
            v-model="dns.strategy">
          </v-select>
        </v-col>
        <v-col cols="12" sm="6" md="3" lg="2">
          <v-text-field
            v-model="dns.client_subnet" hide-details
            clearable @click:clear="delete dns.client_subnet"
            :label="$t('dns.rule.action.clientSubnet')"></v-text-field>
        </v-col>
        <v-col cols="auto">
          <v-text-field
            v-model.number="dns.cache_capacity"
            type="number" min="1024" hide-details
            clearable @click:clear="delete dns.cache_capacity"
            :label="$t('dns.cacheCapacity')"></v-text-field>
        </v-col>
        <v-col cols="auto">
          <v-checkbox v-model="dns.disable_cache" hide-details :label="$t('dns.disableCache')" />
        </v-col>
        <v-col cols="auto">
          <v-checkbox v-model="dns.disable_expire" hide-details :label="$t('dns.disableExpire')" />
        </v-col>
        <v-col cols="auto">
          <v-checkbox v-model="dns.independent_cache" hide-details :label="$t('dns.independentCache')" />
        </v-col>
        <v-col cols="auto">
          <v-checkbox v-model="dns.reverse_mapping" hide-details :label="$t('dns.reverseMapping')" />
        </v-col>
      </v-row>
    </v-col>
  </v-row>
  <v-row>
    <v-col class="v-card-subtitle" cols="12">
      <v-row align="center" no-gutters>
        <v-col cols="auto">{{ $t('dns.title') }}</v-col>
        <v-spacer></v-spacer>
        <v-col cols="12" sm="6" md="4" lg="3">
          <v-text-field
            v-model="searchDnsServers"
            :label="$t('search') || 'Search'"
            prepend-inner-icon="mdi-magnify"
            variant="outlined"
            density="compact"
            hide-details
            clearable
          ></v-text-field>
        </v-col>
      </v-row>
      <v-row v-if="selectedDnsServers.length > 0" class="mt-2" no-gutters>
        <v-col cols="auto" class="d-flex align-center ga-2">
          <v-chip color="primary" variant="tonal">已选 {{ selectedDnsServers.length }}</v-chip>
          <v-btn color="error" size="small" variant="outlined" @click="batchDelDnsConfirm = true">
            {{ $t('actions.deleteSelected') || 'Delete Selected' }}
          </v-btn>
          <v-btn size="small" variant="outlined" @click="selectedDnsServers = []">{{ $t('actions.clearSelection') || 'Clear' }}</v-btn>
        </v-col>
      </v-row>
    </v-col>
    <v-col cols="12" sm="4" md="3" lg="2" v-for="(entry, fidx) in filteredDnsServers" :key="entry.item.id">
      <v-card rounded="xl" elevation="5" min-width="200" :title="entry.item.tag">
        <v-checkbox
          :model-value="selectedDnsServers.includes(entry.index)"
          @update:model-value="toggleDnsServerSelect(entry.index, !!$event)"
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
            <v-col>{{ $t('dns.server') }}</v-col>
            <v-col>
              {{ entry.item.server?? '-' }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('in.port') }}</v-col>
            <v-col>
              {{ entry.item.server_port?? '-' }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('objects.tls') }}</v-col>
            <v-col>
              {{ Object.hasOwn(entry.item,'tls') ? $t(entry.item.tls?.enabled ? 'enable' : 'disable') : '-'  }}
            </v-col>
          </v-row>
        </v-card-text>
        <v-divider></v-divider>
        <v-card-actions style="padding: 0;">
          <v-btn icon="mdi-file-edit" @click="showDnsModal(entry.index)">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.edit')"></v-tooltip>
          </v-btn>
          <v-btn icon="mdi-file-remove" style="margin-inline-start:0;" color="warning" @click="delDnsOverlay[entry.index] = true">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.del')"></v-tooltip>
          </v-btn>
          <v-overlay
            v-model="delDnsOverlay[entry.index]"
            contained
            class="align-center justify-center"
          >
            <v-card :title="$t('actions.del')" rounded="lg">
              <v-divider></v-divider>
              <v-card-text>{{ $t('confirm') }}</v-card-text>
              <v-card-actions>
                <v-btn color="error" variant="outlined" @click="delDns(entry.index)">{{ $t('yes') }}</v-btn>
                <v-btn color="success" variant="outlined" @click="delDnsOverlay[entry.index] = false">{{ $t('no') }}</v-btn>
              </v-card-actions>
            </v-card>
          </v-overlay>
        </v-card-actions>
      </v-card>
    </v-col>
  </v-row>
  <v-row>
    <v-col class="v-card-subtitle" cols="12">
      <v-row align="center" no-gutters>
        <v-col cols="auto">{{ $t('dns.rule.title') }}</v-col>
        <v-spacer></v-spacer>
        <v-col cols="12" sm="6" md="4" lg="3">
          <v-text-field
            v-model="searchDnsRules"
            :label="$t('search') || 'Search'"
            prepend-inner-icon="mdi-magnify"
            variant="outlined"
            density="compact"
            hide-details
            clearable
          ></v-text-field>
        </v-col>
      </v-row>
      <v-row v-if="selectedDnsRules.length > 0" class="mt-2" no-gutters>
        <v-col cols="auto" class="d-flex align-center ga-2">
          <v-chip color="primary" variant="tonal">已选 {{ selectedDnsRules.length }}</v-chip>
          <v-btn color="error" size="small" variant="outlined" @click="batchDelDnsRuleConfirm = true">
            {{ $t('actions.deleteSelected') || 'Delete Selected' }}
          </v-btn>
          <v-btn size="small" variant="outlined" @click="selectedDnsRules = []">{{ $t('actions.clearSelection') || 'Clear' }}</v-btn>
        </v-col>
      </v-row>
    </v-col>
    <v-col cols="12" sm="4" md="3" lg="2" v-for="(entry, fidx) in filteredDnsRules"
      :key="entry.index"
      :draggable="true"
      @dragstart="onDragStart(entry.index)"
      @dragover.prevent
      @drop="onDrop(entry.index)"
      >
      <v-card rounded="xl" elevation="5" min-width="200" :title="entry.index+1">
        <v-checkbox
          :model-value="selectedDnsRules.includes(entry.index)"
          @update:model-value="toggleDnsRuleSelect(entry.index, !!$event)"
          hide-details
          density="compact"
          class="ml-2 mt-1"
        ></v-checkbox>
        <v-card-subtitle style="margin-top: -20px;">
          <v-row>
            <v-col>{{ entry.item.type != undefined ? $t('rule.logical') + ' (' + entry.item.mode + ')' : $t('rule.simple') }}</v-col>
          </v-row>
        </v-card-subtitle>
        <v-card-text>
          <v-row>
            <v-col>{{ $t('admin.action') }}</v-col>
            <v-col>
              {{ entry.item.action }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('dns.server') }}</v-col>
            <v-col>
              {{ entry.item.server?? '-' }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('pages.rules') }}</v-col>
            <v-col>
              {{ entry.item.rules ? entry.item.rules.length : Object.keys(entry.item).filter(r => !actionDnsRuleKeys.includes(r)).length }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('rule.invert') }}</v-col>
            <v-col>
              {{ $t( (entry.item.invert?? false)? 'yes' : 'no') }}
            </v-col>
          </v-row>
        </v-card-text>
        <v-divider></v-divider>
        <v-card-actions style="padding: 0;">
          <v-btn icon="mdi-file-edit" @click="showDnsRuleModal(entry.index)">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.edit')"></v-tooltip>
          </v-btn>
          <v-btn icon="mdi-file-remove" style="margin-inline-start:0;" color="warning" @click="delDnsRuleOverlay[entry.index] = true">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.del')"></v-tooltip>
          </v-btn>
          <v-overlay
            v-model="delDnsRuleOverlay[entry.index]"
            contained
            class="align-center justify-center"
          >
            <v-card :title="$t('actions.del')" rounded="lg">
              <v-divider></v-divider>
              <v-card-text>{{ $t('confirm') }}</v-card-text>
              <v-card-actions>
                <v-btn color="error" variant="outlined" @click="delDnsRule(entry.index)">{{ $t('yes') }}</v-btn>
                <v-btn color="success" variant="outlined" @click="delDnsRuleOverlay[entry.index] = false">{{ $t('no') }}</v-btn>
              </v-card-actions>
            </v-card>
          </v-overlay>
        </v-card-actions>
      </v-card>
    </v-col>
  </v-row>
  <v-dialog v-model="batchDelDnsConfirm" max-width="400">
    <v-card :title="$t('actions.del')" rounded="lg">
      <v-divider></v-divider>
      <v-card-text>{{ $t('actions.confirmDeleteSelected', { count: selectedDnsServers.length }) || `Delete ${selectedDnsServers.length} selected DNS servers?` }}</v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="error" variant="outlined" @click="batchDelDnsServers">{{ $t('yes') }}</v-btn>
        <v-btn color="success" variant="outlined" @click="batchDelDnsConfirm = false">{{ $t('no') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  <v-dialog v-model="batchDelDnsRuleConfirm" max-width="400">
    <v-card :title="$t('actions.del')" rounded="lg">
      <v-divider></v-divider>
      <v-card-text>{{ $t('actions.confirmDeleteSelected', { count: selectedDnsRules.length }) || `Delete ${selectedDnsRules.length} selected DNS rules?` }}</v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="error" variant="outlined" @click="batchDelDnsRules">{{ $t('yes') }}</v-btn>
        <v-btn color="success" variant="outlined" @click="batchDelDnsRuleConfirm = false">{{ $t('no') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script lang="ts" setup>
import Data from '@/store/modules/data'
import { computed, ref, onMounted } from 'vue'
import DnsVue from '@/layouts/modals/Dns.vue'
import DnsRuleVue from '@/layouts/modals/DnsRule.vue'
import { Config } from '@/types/config'
import { actionDnsRuleKeys, dnsRule } from '@/types/dns'
import { FindDiff } from '@/plugins/utils'

const oldConfig = ref(<any>{})
const loading = ref(false)

const appConfig = computed((): Config => {
  return <Config> Data().config
})

onMounted(() => {
  // fix old configs
  if (!appConfig.value.dns) appConfig.value.dns = { servers: [], rules: [] }
  if (!appConfig.value.dns.servers) appConfig.value.dns.servers = []
  if (!appConfig.value.dns.rules) appConfig.value.dns.rules = []

  oldConfig.value = JSON.parse(JSON.stringify(Data().config))
})

const tsTags = computed((): string[] => {
  return Data().endpoints?.filter((e:any) => e.type == "tailscale").map((e:any) => e.tag)
})

const rslvdTags = computed((): string[] => {
  return Data().services?.filter((e:any) => e.type == "resolved").map((e:any) => e.tag)
})

const clients = computed((): string[] => {
  return Data().clients.map((c:any) => c.name)
})

const stateChange = computed(() => {
  return FindDiff.deepCompare(appConfig.value.dns,oldConfig.value.dns)
})

const saveConfig = async () => {
  loading.value = true
  const success = await Data().save("config", "set", appConfig.value)
  if (success) {
    oldConfig.value = JSON.parse(JSON.stringify(Data().config))
  }
  loading.value = false
}

const inboundTags = computed((): string[] => {
  return [...Data().inbounds?.map((o:any) => o.tag), ...Data().endpoints?.filter((e:any) => e.listen_port > 0).map((e:any) => e.tag)]
})

const dns = computed((): any => {
  return appConfig.value.dns
})

const dnsServerTags = computed((): string[] => {
  return dns.value?.servers?.filter((s:any) => s.tag && s.tag != "")?.map((s:any) => s.tag) ?? []
})

const finalDns = computed({
  get() { return dns.value?.final?? '' },
  set(v:string) { dns.value.final = v.length>0 ? v : undefined }
})


const dnsRules = computed((): dnsRule[] => {
  return <dnsRule[]>dns.value.rules
})

const ruleSets = computed((): string[] => {
  return appConfig.value?.route?.rule_set?.map((r:any) => r.tag) ?? []
})

let delDnsOverlay = ref(new Array<boolean>)
let delDnsRuleOverlay = ref(new Array<boolean>)

// Search & batch selection for DNS servers and DNS rules
const searchDnsServers = ref('')
const searchDnsRules = ref('')
const selectedDnsServers = ref<number[]>([])
const selectedDnsRules = ref<number[]>([])
const batchDelDnsConfirm = ref(false)
const batchDelDnsRuleConfirm = ref(false)

const filteredDnsServers = computed((): {item: any, index: number}[] => {
  const servers: any[] = dns.value?.servers ?? []
  const all = servers.map((item: any, index: number) => ({ item, index }))
  if (!searchDnsServers.value) return all
  const q = searchDnsServers.value.toLowerCase()
  return all.filter(({ item }) =>
    (item.tag && String(item.tag).toLowerCase().includes(q)) ||
    (item.type && String(item.type).toLowerCase().includes(q)) ||
    (item.server && String(item.server).toLowerCase().includes(q))
  )
})

const filteredDnsRules = computed((): {item: any, index: number}[] => {
  const all = dnsRules.value.map((item: any, index: number) => ({ item, index }))
  if (!searchDnsRules.value) return all
  const q = searchDnsRules.value.toLowerCase()
  return all.filter(({ item }) =>
    (item.action && String(item.action).toLowerCase().includes(q)) ||
    (item.server && String(item.server).toLowerCase().includes(q)) ||
    (item.type && String(item.type).toLowerCase().includes(q))
  )
})

const toggleDnsServerSelect = (index: number, val: boolean) => {
  if (val) {
    if (!selectedDnsServers.value.includes(index)) selectedDnsServers.value.push(index)
  } else {
    selectedDnsServers.value = selectedDnsServers.value.filter(i => i !== index)
  }
}

const toggleDnsRuleSelect = (index: number, val: boolean) => {
  if (val) {
    if (!selectedDnsRules.value.includes(index)) selectedDnsRules.value.push(index)
  } else {
    selectedDnsRules.value = selectedDnsRules.value.filter(i => i !== index)
  }
}

const batchDelDnsServers = () => {
  const idx = [...selectedDnsServers.value].sort((a, b) => b - a)
  for (const i of idx) dns.value.servers.splice(i, 1)
  selectedDnsServers.value = []
  batchDelDnsConfirm.value = false
}

const batchDelDnsRules = () => {
  const idx = [...selectedDnsRules.value].sort((a, b) => b - a)
  for (const i of idx) dnsRules.value.splice(i, 1)
  selectedDnsRules.value = []
  batchDelDnsRuleConfirm.value = false
}

const dnsModal = ref({
  visible: false,
  index: -1,
  data: "",
})

const showDnsModal = (index: number) => {
  dnsModal.value.index = index
  dnsModal.value.data = index == -1 ? '' : JSON.stringify(dns.value.servers[index])
  dnsModal.value.visible = true
}

const closeDnsModal = () => {
  dnsModal.value.visible = false
}

const saveDnsModal = (data:any) => {
  // New or Edit
  if (dnsModal.value.index == -1) {
    dns.value.servers.push(data)
  } else {
    dns.value.servers[dnsModal.value.index] = data
  }
  dnsModal.value.visible = false
}

const delDns = (index: number) => {
  dns.value.servers.splice(index,1)
  delDnsOverlay.value[index] = false
}

const dnsRuleModal = ref({
  visible: false,
  index: -1,
  data: "",
})

const showDnsRuleModal = (index: number) => {
  dnsRuleModal.value.index = index
  dnsRuleModal.value.data = index == -1 ? '' : JSON.stringify(dnsRules.value[index])
  dnsRuleModal.value.visible = true
}

const closeDnsRuleModal = () => {
  dnsRuleModal.value.visible = false
}

const saveDnsRuleModal = (data:dnsRule) => {
  // New or Edit
  if (dnsRuleModal.value.index == -1) {
    dnsRules.value.push(data)
  } else {
    dnsRules.value[dnsRuleModal.value.index] = data
  }
  dnsRuleModal.value.visible = false
}

const delDnsRule = (index: number) => {
  dnsRules.value.splice(index,1)
  delDnsRuleOverlay.value[index] = false
}

const draggedItemIndex = ref(null)

const onDragStart = (index: any) => {
  draggedItemIndex.value = index
}

const onDrop = (index: any) => {
  if (draggedItemIndex.value !== null) {
    // Swap the dragged item with the dropped one
    const draggedItem = dnsRules.value[draggedItemIndex.value]
    dnsRules.value.splice(draggedItemIndex.value, 1)
    dnsRules.value.splice(index, 0, draggedItem)
    draggedItemIndex.value = null
  }
}
</script>