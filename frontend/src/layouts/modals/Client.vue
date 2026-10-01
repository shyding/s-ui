<template>
  <v-dialog transition="dialog-bottom-transition" width="800">
    <v-card class="rounded-lg" :loading="loading">
      <v-card-title>
        {{ $t('actions.' + title) + " " + $t('objects.client') }}
      </v-card-title>
      <v-divider></v-divider>
      <v-skeleton-loader
          class="mx-auto border"
          width="95%"
          type="card, text, divider, list-item-two-line"
          v-if="loading"
        ></v-skeleton-loader>
      <v-card-text style="padding: 0 16px; overflow-y: scroll;">
        <v-container style="padding: 0;" :hidden="loading">
          <v-tabs
            v-model="tab"
            align-tabs="center"
          >
            <v-tab value="t1">{{ $t('client.basics') }}</v-tab>
            <v-tab value="t2">{{ $t('client.config') }}</v-tab>
            <v-tab value="t3">{{ $t('client.links') }}</v-tab>
          </v-tabs>
          <v-window v-model="tab">
            <v-window-item value="t1">
              <v-row>
                <v-col cols="12" sm="6" md="4">
                  <v-switch color="primary" v-model="client.enable" :label="$t('enable')" hide-details></v-switch>
                </v-col>
                <v-col cols="12" sm="6" md="4">
                  <v-combobox v-model="client.group" :items="groups" :label="$t('client.group')" hide-details></v-combobox>
                </v-col>
              </v-row>
              <v-row>
                <v-col cols="12" sm="6" md="4">
                  <v-text-field v-model="client.name" :label="$t('client.name')" hide-details></v-text-field>
                </v-col>
                <v-col cols="12" sm="6" md="4">
                  <v-text-field v-model="client.desc" :label="$t('client.desc')" hide-details></v-text-field>
                </v-col>
              </v-row>
              <v-row>
                <v-col cols="12" sm="6" md="4">
                  <v-text-field v-model.number="Volume" type="number" min="0" :label="$t('stats.volume')" suffix="GiB" hide-details></v-text-field>
                </v-col>
                <v-col cols="12" sm="6" md="4">
                  <DatePick :expiry="expDate" @submit="setDate" />
                </v-col>
              </v-row>
              <v-row v-if="id > 0">
                <v-col cols="12" sm="6" md="4" class="d-flex flex-column">
                  <div class="d-flex justify-space-between align-center">
                    <div>
                      {{ $t('stats.usage') }}: {{ total }}<sup dir="ltr" v-if="percent>0">({{ percent }}%)</sup>
                    </div>
                    <v-btn density="compact" variant="text" icon="mdi-restore" @click="client.up=0;client.down=0">
                      <v-tooltip activator="parent" location="top">
                        {{ $t('reset') }}
                      </v-tooltip>
                      <v-icon />
                    </v-btn>
                  </div>
                  <v-progress-linear
                    v-model="percent"
                    :color="percentColor"
                    v-if="client.volume>0"
                    bottom
                  >
                  </v-progress-linear>
                </v-col>
                <v-col cols="12" sm="6" md="4">
                  <v-icon icon="mdi-upload" color="orange" /><span class="text-orange">{{ up }}</span>
                  / 
                  <v-icon icon="mdi-download" color="success" /><span class="text-success">{{ down }}</span>
                </v-col>
              </v-row>
              <v-row>
                <v-col>
                  <v-select
                    v-model="clientInbounds"
                    :items="inboundTags"
                    :label="$t('client.inboundTags')"
                    clearable
                    multiple
                    chips
                    hide-details>
                    <template v-slot:append>
                      <v-icon @click="setAllInbounds" icon="mdi-set-all" v-tooltip:top="$t('all')" />
                    </template>
                  </v-select>
                </v-col>
              </v-row>
              <v-row>
                <v-col cols="auto">
                  <v-btn variant="tonal" color="primary" :loading="suiLoading" @click="openSUIDialog">
                    <v-icon icon="mdi-server-network" start />选择SUI节点
                  </v-btn>
                </v-col>
                <v-col cols="auto" v-if="suiCount > 0">
                  <span class="text-caption text-medium-emphasis">已识别 {{ suiCount }}/38 个SUI节点</span>
                </v-col>
              </v-row>
            </v-window-item>
            <v-window-item value="t2">
              <v-row>
                <v-col cols="12" sm="6" md="4">
                  <v-btn variant="tonal" @click="shuffle()">{{ $t('reset') + ' - ' + $t('all') }}<v-icon icon="mdi-refresh" /></v-btn>
                </v-col>
              </v-row>
              <v-row v-for="key in Object.keys(clientConfig)">
                <v-col cols="12" md="3" align="end" align-self="center">
                    {{ key }}
                    <v-icon @click="shuffle(key)" icon="mdi-refresh" v-tooltip:top="$t('reset')" />
                </v-col>
                <v-col>
                  <v-text-field
                    v-if="clientConfig[key].password != undefined"
                    label="Password"
                    v-model="clientConfig[key].password"
                    hide-details>
                  </v-text-field>
                  <v-text-field
                    v-if="clientConfig[key].uuid != undefined"
                    label="UUID"
                    v-model="clientConfig[key].uuid"
                    hide-details>
                  </v-text-field>
                  <v-text-field
                    v-if="key == 'vless'"
                    label="Flow"
                    v-model="clientConfig[key].flow"
                    hide-details>
                  </v-text-field>
                  <v-text-field
                    v-if="key == 'hysteria'"
                    label="Auth"
                    v-model="clientConfig[key].auth_str"
                    hide-details>
                  </v-text-field>
                </v-col>
              </v-row>
            </v-window-item>
            <v-window-item value="t3">
              <v-row v-for="(lnk, index) in links">
                <v-col cols="auto">{{ index + 1 }}</v-col>
                <v-col style="direction: ltr; overflow-y: hidden;">{{ lnk.uri }}</v-col>
              </v-row>
              <v-row>
                <v-col>
                  <v-btn color="primary" @click="extLinks.push({ type: 'external', uri: ''})">{{ $t('actions.add') }} {{ $t('client.external') }}</v-btn>
                </v-col>
              </v-row>
              <v-row v-for="(lnk, index) in extLinks">
                <v-col>
                  <v-text-field
                  dir="ltr"
                  :label="$t('client.external') + ' ' + (index+1)"
                  append-icon="mdi-delete"
                  @click:append="extLinks.splice(index,1)"
                  placeholder="<protocol>://<data>"
                  v-model="lnk.uri" />
                </v-col>
              </v-row>
              <v-row>
                <v-col>
                  <v-btn color="primary" @click="subLinks.push({ type: 'sub', uri: ''})">{{ $t('actions.add') }} {{ $t('client.sub') }}</v-btn>
                </v-col>
              </v-row>
              <v-row v-for="(lnk, index) in subLinks">
                <v-col>
                  <v-text-field
                  dir="ltr"
                  :label="$t('client.sub') + ' ' + (index+1)"
                  append-icon="mdi-delete"
                  @click:append="subLinks.splice(index,1)"
                  placeholder="http[s]://<domain>[:]<port>/<path>"
                  v-model="lnk.uri" />
                </v-col>
              </v-row>
            </v-window-item>
          </v-window>
        </v-container>
      </v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn
          color="primary"
          variant="outlined"
          @click="closeModal"
        >
          {{ $t('actions.close') }}
        </v-btn>
        <v-btn
          color="primary"
          variant="tonal"
          :loading="loading"
          @click="saveChanges"
        >
          {{ $t('actions.save') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- SUI节点选择对话框：支持勾选部分或全部38个节点（端口各不相同） -->
  <v-dialog v-model="suiDialog" max-width="900px" scrollable>
    <v-card>
      <v-card-title class="d-flex align-center">
        <span>选择SUI节点</span>
        <v-spacer></v-spacer>
        <v-btn size="small" variant="text" @click="selectAllSUI">全选</v-btn>
        <v-btn size="small" variant="text" @click="clearSUI">清空</v-btn>
      </v-card-title>
      <v-card-text style="max-height: 60vh">
        <div v-if="suiNodes.length === 0" class="text-center pa-4">
          <v-progress-circular indeterminate></v-progress-circular>
          <div class="mt-2">加载中...</div>
        </div>
        <div v-for="group in suiGroups" :key="group.name">
          <v-row class="align-center" no-gutters>
            <v-col cols="auto">
              <v-checkbox
                :model-value="groupSelected(group)"
                :indeterminate="groupIndeterminate(group)"
                @update:model-value="toggleGroup(group, $event)"
                :label="group.label + ' (' + group.nodes.length + ')'"
                hide-details
                density="compact"
              ></v-checkbox>
            </v-col>
          </v-row>
          <v-row no-gutters>
            <v-col v-for="node in group.nodes" :key="node.id" cols="12" sm="6" md="4">
              <v-checkbox
                v-model="suiSelected"
                :value="node.id"
                :disabled="node.id === 0"
                :label="node.tag + ' :' + node.port"
                hide-details
                density="compact"
              ></v-checkbox>
            </v-col>
          </v-row>
          <v-divider class="my-2"></v-divider>
        </div>
      </v-card-text>
      <v-card-actions>
        <span class="text-caption text-medium-emphasis ml-2">已选 {{ suiSelected.length }} 个</span>
        <v-spacer></v-spacer>
        <v-btn variant="text" @click="suiDialog = false">取消</v-btn>
        <v-btn color="primary" variant="tonal" @click="applySUISelection">确定</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script lang="ts">
import { createClient, randomConfigs, updateConfigs, Link, shuffleConfigs } from '@/types/clients'
import DatePick from '@/components/DateTime.vue'
import { HumanReadable } from '@/plugins/utils'
import HttpUtils from '@/plugins/httputil'
import Data from '@/store/modules/data'

export default {
  props: ['visible', 'id', 'inboundTags', 'groups'],
  emits: ['close'],
  data() {
    return {
      client: createClient(),
      title: "add",
      loading: false,
      tab: "t1",
      clientConfig: <any>[],
      links: <Link[]>[],
      extLinks: <Link[]>[],
      subLinks: <Link[]>[],
      suiLoading: false,
      suiCount: 0,
      suiDialog: false,
      suiNodes: [] as any[],
      suiSelected: [] as number[],
    }
  },
  methods: {
    async updateData(id: number) {
      if (id > 0) {
        this.loading = true
        const newData = await Data().loadClients(id)
        this.client = createClient(newData)
        this.title = "edit"
        this.clientConfig = this.client.config
        this.loading = false
      }
      else {
        this.client = createClient()
        this.title = "add"
        this.clientConfig = randomConfigs('client')
      }
      this.links = this.client.links?.filter(l => l.type == 'local')?? []
      this.extLinks = this.client.links?.filter(l => l.type == 'external')?? []
      this.subLinks = this.client.links?.filter(l => l.type == 'sub')?? []
      this.tab = "t1"
      this.loading = false
    },
    closeModal() {
      this.updateData(0) // reset
      this.$emit('close')
    },
    async saveChanges() {
      if (!this.$props.visible) return
      // check duplicate name
      const isDuplicateName = Data().checkClientName(this.$props.id, this.client.name)
      if (isDuplicateName) return

      // save data
      this.loading = true
      this.client.config = updateConfigs(this.clientConfig, this.client.name)
      this.client.links = [
                        ...this.extLinks.filter(l => l.uri != ''),
                        ...this.subLinks.filter(l => l.uri != '')]
      const success = await Data().save("clients", this.$props.id == 0 ? "new" : "edit", this.client)
      if (success) this.closeModal()
      this.loading = false
    },
    setDate(newDate:number){
      this.client.expiry = newDate
    },
    setAllInbounds(){
      this.client.inbounds = this.inboundTags.map((i:any) => i.value).sort()
    },
    async openSUIDialog(){
      // 打开SUI节点选择对话框（支持勾选部分或全部）
      this.suiDialog = true
      this.suiLoading = true
      try {
        const msg = await HttpUtils.get('api/suiNodes')
        if (msg.success && msg.obj?.nodes) {
          this.suiNodes = msg.obj.nodes
          this.suiCount = msg.obj.nodes.filter((n:any) => n.id > 0).length
          // 默认勾选当前用户已选中的SUI节点
          this.suiSelected = msg.obj.nodes
            .filter((n:any) => n.id > 0 && this.client.inbounds.includes(n.id))
            .map((n:any) => n.id)
        }
      } finally {
        this.suiLoading = false
      }
    },
    selectAllSUI(){
      this.suiSelected = this.suiNodes.filter((n:any) => n.id > 0).map((n:any) => n.id)
    },
    clearSUI(){
      this.suiSelected = []
    },
    toggleGroup(group:any, selected:boolean | null){
      const ids = group.nodes.filter((n:any) => n.id > 0).map((n:any) => n.id)
      if (selected) {
        this.suiSelected = Array.from(new Set([...this.suiSelected, ...ids]))
      } else {
        this.suiSelected = this.suiSelected.filter((id:number) => !ids.includes(id))
      }
    },
    groupSelected(group:any):boolean{
      const ids = group.nodes.filter((n:any) => n.id > 0).map((n:any) => n.id)
      return ids.length > 0 && ids.every((id:number) => this.suiSelected.includes(id))
    },
    groupIndeterminate(group:any):boolean{
      const ids = group.nodes.filter((n:any) => n.id > 0).map((n:any) => n.id)
      const selected = ids.filter((id:number) => this.suiSelected.includes(id))
      return selected.length > 0 && selected.length < ids.length
    },
    applySUISelection(){
      // 应用选择：将勾选的SUI节点合并到用户inbounds（去重），取消勾选的从SUI集合中移除
      const suiIds = new Set(this.suiNodes.filter((n:any) => n.id > 0).map((n:any) => n.id))
      const selectedSet = new Set(this.suiSelected)
      // 保留非SUI的inbound + 勾选的SUI节点
      const kept = this.client.inbounds.filter((id:number) => !suiIds.has(id))
      this.client.inbounds = Array.from(new Set([...kept, ...selectedSet])).sort((a:number,b:number) => a-b)
      this.suiDialog = false
    },
    shuffle(k?:string) {
      shuffleConfigs(this.clientConfig, k)
    }
  },
  computed: {
    suiGroups(): any[] {
      // 按协议分组SUI节点（每组端口各不相同）
      const order = ['vless', 'vmess', 'trojan', 'hysteria2', 'tuic', 'shadowsocks', 'mixed', 'socks']
      const labels: Record<string, string> = {
        vless: 'VLESS', vmess: 'VMess', trojan: 'Trojan',
        hysteria2: 'Hysteria2', tuic: 'TUIC',
        shadowsocks: 'Shadowsocks', mixed: 'Mixed', socks: 'SOCKS'
      }
      const groups: Record<string, any[]> = {}
      for (const n of this.suiNodes) {
        const p = (n.protocol || 'unknown').toLowerCase()
        if (!groups[p]) groups[p] = []
        groups[p].push(n)
      }
      return Object.keys(groups)
        .sort((a, b) => {
          const ia = order.indexOf(a), ib = order.indexOf(b)
          return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib)
        })
        .map(k => ({ name: k, label: labels[k] || k.toUpperCase(), nodes: groups[k].sort((x:any, y:any) => x.port - y.port) }))
    },
    clientInbounds: {
      get() { return this.client.inbounds.length>0 ? this.client.inbounds.sort() : [] },
      set(v:number[]) { this.client.inbounds = v.length == 0 ?  [] : v.sort() }
    },
    expDate: {
      get() { return this.client.expiry},
      set(v:any) { this.client.expiry = v }
    },
    Volume: {
      get() { return this.client.volume == 0 ? 0 : (this.client.volume / (1024 ** 3)) },
      set(v:number) { this.client.volume = v > 0 ? v*(1024 ** 3) : 0 }
    },
    up() :string { return HumanReadable.sizeFormat(this.client.up) },
    down() :string { return HumanReadable.sizeFormat(this.client.down) },
    total() :string { return HumanReadable.sizeFormat(this.client.down + this.client.up) },
    percent() :number { return this.client.volume>0 ? Math.round((this.client.up + this.client.down) *100 / this.client.volume) : 0 },
    percentColor() :string { return (this.client.up+this.client.down) >= this.client.volume ? 'error' : this.percent>90 ? 'warning' : 'success' },
  },
  watch: {
    visible(newValue) {
      if (newValue) {
        this.updateData(this.$props.id)
      }
    },
  },
  components: { DatePick },
}

</script>