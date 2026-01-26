{#
# Copyright (C) 2025 Your Name
# All rights reserved.
#}

<script>
$(document).ready(function() {
    // Reconfigure after saving
    $("#reconfigureAct").SimpleActionButton({
        onPreAction: function() {
            const dfObj = new $.Deferred();
            saveFormToEndpoint("/api/cloudflared/settings/set", 'frm_settings', function(){
                dfObj.resolve();
            });
            return dfObj;
        },
        onAction: function(data, status) {
            if (status === "success") {
                // Trigger reconfigure
                $.ajax({
                    url: '/api/cloudflared/service/reconfigure',
                    type: 'POST',
                    success: function() {
                        // Show success message
                    }
                });
            }
        }
    });
});
</script>

<div class="content-box">
    <div class="content-box-main">
        <div class="tab-content">
            <div id="general" class="tab-pane fade in active">
                {{ partial('layout_partials/base_form',['fields': generalForm, 'action': '/ui/cloudflared/settings', 'id': 'frm_settings']) }}
            </div>
        </div>
    </div>
</div>

<div class="content-box">
    <div class="content-box-main">
        <div class="table-responsive">
            <table class="table table-striped table-condensed">
                <thead>
                    <tr>
                        <th>{{ lang._('Hostname') }}</th>
                        <th>{{ lang._('Service') }}</th>
                        <th>{{ lang._('URL') }}</th>
                        <th>{{ lang._('Actions') }}</th>
                    </tr>
                </thead>
                <tbody>
                {% for tunnel in tunnels %}
                    <tr>
                        <td>{{ tunnel.hostname }}</td>
                        <td>{{ tunnel.service }}</td>
                        <td>{{ tunnel.url }}</td>
                        <td>
                            <button class="btn btn-xs btn-default" onclick="editTunnel('{{ tunnel['@uuid'] }}')">
                                <i class="fa fa-edit"></i>
                            </button>
                            <button class="btn btn-xs btn-danger" onclick="deleteTunnel('{{ tunnel['@uuid'] }}')">
                                <i class="fa fa-trash"></i>
                            </button>
                        </td>
                    </tr>
                {% endfor %}
                </tbody>
            </table>
        </div>
        <div class="col-md-12">
            <button class="btn btn-primary" onclick="addTunnel()">
                <i class="fa fa-plus"></i> {{ lang._('Add Tunnel') }}
            </button>
            <button id="reconfigureAct" class="btn btn-success">
                <i class="fa fa-check"></i> {{ lang._('Apply Changes') }}
            </button>
        </div>
    </div>
</div>

<script>
function addTunnel() {
    // Open dialog to add new tunnel
    $("#DialogTunnel").modal('show');
    // Reset form
    $("#tunnel_hostname").val('');
    $("#tunnel_service").val('http');
    $("#tunnel_url").val('');
}

function editTunnel(uuid) {
    // Load existing tunnel data and open dialog
    $("#DialogTunnel").modal('show');
    // Load data for editing
}

function deleteTunnel(uuid) {
    // Confirm and delete tunnel
    if (confirm('{{ lang._('Do you want to delete this tunnel?') }}')) {
        // Delete tunnel
    }
}
</script>

<div class="modal fade" id="DialogTunnel" tabindex="-1" role="dialog">
    <div class="modal-dialog modal-lg" role="document">
        <div class="modal-content">
            <div class="modal-header">
                <button type="button" class="close" data-dismiss="modal" aria-label="Close">
                    <span aria-hidden="true">&times;</span>
                </button>
                <h4 class="modal-title">{{ lang._('Edit Tunnel') }}</h4>
            </div>
            <div class="modal-body">
                {{ partial('layout_partials/base_form',['fields': tunnelForm, 'id': 'frm_tunnel']) }}
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-default" data-dismiss="modal">{{ lang._('Cancel') }}</button>
                <button type="button" class="btn btn-primary" onclick="saveTunnel()">{{ lang._('Save') }}</button>
            </div>
        </div>
    </div>
</div>