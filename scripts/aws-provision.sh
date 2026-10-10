#!/bin/bash
#
# FlashDB Benchmark AWS Infrastructure Provisioner
#
# Creates:
#   - VPC with public subnet
#   - Security groups (FlashDB, Redis, Monitoring, SSH)
#   - 3x m7i-flex.large EC2 instances
#   - S3 bucket for benchmark artifacts
#   - Budget alarm at $30
#
# Usage:
#   ./aws-provision.sh create    # Create all infrastructure
#   ./aws-provision.sh start     # Start stopped instances
#   ./aws-provision.sh stop      # Stop instances (saves money)
#   ./aws-provision.sh status    # Show instance status and IPs
#   ./aws-provision.sh destroy   # Terminate everything (CAREFUL!)
#   ./aws-provision.sh ssh <1|2|3>  # SSH into instance 1, 2, or 3
#
# Prerequisites:
#   - AWS CLI configured with appropriate credentials
#   - SSH key pair created in us-east-1 (will prompt if missing)

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Configuration
# ─────────────────────────────────────────────────────────────────────────────

REGION="us-east-1"
PROJECT_NAME="flashdb-bench"
INSTANCE_TYPE="m7i-flex.large"
KEY_NAME="${PROJECT_NAME}-key"
S3_BUCKET="${PROJECT_NAME}-artifacts-$(aws sts get-caller-identity --query Account --output text)"

# Amazon Linux 2023 AMI (us-east-1) - will be looked up dynamically
AMI_ID=""

# Tag for resource identification
TAG_KEY="Project"
TAG_VALUE="$PROJECT_NAME"

# State file to track created resources
STATE_FILE="$HOME/.flashdb-bench-state.json"

# ─────────────────────────────────────────────────────────────────────────────
# Helper Functions
# ─────────────────────────────────────────────────────────────────────────────

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1"
}

error() {
    echo "[ERROR] $1" >&2
    exit 1
}

save_state() {
    echo "$1" > "$STATE_FILE"
}

load_state() {
    if [[ -f "$STATE_FILE" ]]; then
        cat "$STATE_FILE"
    else
        echo "{}"
    fi
}

get_state_value() {
    local key="$1"
    load_state | jq -r ".$key // empty"
}

# ─────────────────────────────────────────────────────────────────────────────
# Create Infrastructure
# ─────────────────────────────────────────────────────────────────────────────

create_infrastructure() {
    log "Creating FlashDB benchmark infrastructure in $REGION..."

    # Lookup latest Amazon Linux 2023 AMI
    log "Looking up Amazon Linux 2023 AMI..."
    AMI_ID=$(aws ec2 describe-images \
        --region "$REGION" \
        --owners amazon \
        --filters "Name=name,Values=al2023-ami-2023*-x86_64" \
                  "Name=state,Values=available" \
        --query 'sort_by(Images, &CreationDate)[-1].ImageId' \
        --output text)
    log "Using AMI: $AMI_ID"

    # Create VPC
    log "Creating VPC..."
    VPC_ID=$(aws ec2 create-vpc \
        --region "$REGION" \
        --cidr-block "10.0.0.0/16" \
        --tag-specifications "ResourceType=vpc,Tags=[{Key=$TAG_KEY,Value=$TAG_VALUE},{Key=Name,Value=$PROJECT_NAME-vpc}]" \
        --query 'Vpc.VpcId' \
        --output text)
    log "Created VPC: $VPC_ID"

    # Enable DNS hostnames
    aws ec2 modify-vpc-attribute --region "$REGION" --vpc-id "$VPC_ID" --enable-dns-hostnames

    # Create Internet Gateway
    log "Creating Internet Gateway..."
    IGW_ID=$(aws ec2 create-internet-gateway \
        --region "$REGION" \
        --tag-specifications "ResourceType=internet-gateway,Tags=[{Key=$TAG_KEY,Value=$TAG_VALUE},{Key=Name,Value=$PROJECT_NAME-igw}]" \
        --query 'InternetGateway.InternetGatewayId' \
        --output text)
    aws ec2 attach-internet-gateway --region "$REGION" --vpc-id "$VPC_ID" --internet-gateway-id "$IGW_ID"
    log "Created and attached IGW: $IGW_ID"

    # Create public subnet
    log "Creating public subnet..."
    SUBNET_ID=$(aws ec2 create-subnet \
        --region "$REGION" \
        --vpc-id "$VPC_ID" \
        --cidr-block "10.0.1.0/24" \
        --availability-zone "${REGION}a" \
        --tag-specifications "ResourceType=subnet,Tags=[{Key=$TAG_KEY,Value=$TAG_VALUE},{Key=Name,Value=$PROJECT_NAME-subnet}]" \
        --query 'Subnet.SubnetId' \
        --output text)
    aws ec2 modify-subnet-attribute --region "$REGION" --subnet-id "$SUBNET_ID" --map-public-ip-on-launch
    log "Created subnet: $SUBNET_ID"

    # Create route table and route to IGW
    log "Creating route table..."
    RTB_ID=$(aws ec2 create-route-table \
        --region "$REGION" \
        --vpc-id "$VPC_ID" \
        --tag-specifications "ResourceType=route-table,Tags=[{Key=$TAG_KEY,Value=$TAG_VALUE},{Key=Name,Value=$PROJECT_NAME-rtb}]" \
        --query 'RouteTable.RouteTableId' \
        --output text)
    aws ec2 create-route --region "$REGION" --route-table-id "$RTB_ID" --destination-cidr-block "0.0.0.0/0" --gateway-id "$IGW_ID"
    aws ec2 associate-route-table --region "$REGION" --route-table-id "$RTB_ID" --subnet-id "$SUBNET_ID"
    log "Created route table: $RTB_ID"

    # Create security group
    log "Creating security group..."
    SG_ID=$(aws ec2 create-security-group \
        --region "$REGION" \
        --vpc-id "$VPC_ID" \
        --group-name "$PROJECT_NAME-sg" \
        --description "FlashDB benchmark security group" \
        --tag-specifications "ResourceType=security-group,Tags=[{Key=$TAG_KEY,Value=$TAG_VALUE},{Key=Name,Value=$PROJECT_NAME-sg}]" \
        --query 'GroupId' \
        --output text)

    # Allow SSH from anywhere (for your access)
    aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
        --protocol tcp --port 22 --cidr "0.0.0.0/0"

    # Allow all traffic within the VPC (for inter-instance communication)
    aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
        --protocol -1 --source-group "$SG_ID"

    # Allow Grafana from anywhere (for dashboard access)
    aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
        --protocol tcp --port 3000 --cidr "0.0.0.0/0"

    # Allow Prometheus from anywhere (optional, for debugging)
    aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
        --protocol tcp --port 9090 --cidr "0.0.0.0/0"

    log "Created security group: $SG_ID"

    # Create or use existing key pair
    log "Checking key pair..."
    if ! aws ec2 describe-key-pairs --region "$REGION" --key-names "$KEY_NAME" &>/dev/null; then
        log "Creating new key pair..."
        aws ec2 create-key-pair \
            --region "$REGION" \
            --key-name "$KEY_NAME" \
            --query 'KeyMaterial' \
            --output text > "$HOME/.ssh/${KEY_NAME}.pem"
        chmod 400 "$HOME/.ssh/${KEY_NAME}.pem"
        log "Created key pair: $KEY_NAME (saved to ~/.ssh/${KEY_NAME}.pem)"
    else
        log "Using existing key pair: $KEY_NAME"
    fi

    # User data scripts for each instance
    FLASHDB_USER_DATA=$(cat <<'EOF'
#!/bin/bash
yum update -y
yum install -y docker git
systemctl enable docker
systemctl start docker
usermod -aG docker ec2-user

# Install Go
wget https://go.dev/dl/go1.25.0.linux-amd64.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile.d/go.sh

# Clone and build FlashDB
cd /home/ec2-user
git clone https://github.com/shraj19/flashdb.git || true
cd flashdb
/usr/local/go/bin/go build -o flashdb .
chown -R ec2-user:ec2-user /home/ec2-user/flashdb

# Create systemd service for FlashDB
cat > /etc/systemd/system/flashdb.service <<'UNIT'
[Unit]
Description=FlashDB Server
After=network.target

[Service]
Type=simple
User=ec2-user
WorkingDirectory=/home/ec2-user/flashdb
ExecStart=/home/ec2-user/flashdb/flashdb --port 6379 --metrics-port 9121
Restart=always

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable flashdb
systemctl start flashdb
EOF
)

    REDIS_USER_DATA=$(cat <<'EOF'
#!/bin/bash
yum update -y
yum install -y docker
systemctl enable docker
systemctl start docker
usermod -aG docker ec2-user

# Run Redis and redis_exporter
docker run -d --name redis -p 6379:6379 --restart unless-stopped redis:7-alpine
docker run -d --name redis_exporter -p 9121:9121 --restart unless-stopped \
    -e REDIS_ADDR=redis://172.17.0.1:6379 \
    oliver006/redis_exporter:latest
EOF
)

    MONITORING_USER_DATA=$(cat <<'EOF'
#!/bin/bash
yum update -y
yum install -y docker git
systemctl enable docker
systemctl start docker
usermod -aG docker ec2-user

# Install docker-compose
curl -L "https://github.com/docker/compose/releases/latest/download/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
chmod +x /usr/local/bin/docker-compose

# Install redis-benchmark
amazon-linux-extras install redis6 -y || yum install -y redis6 || yum install -y redis

# Clone FlashDB repo for compose files
cd /home/ec2-user
git clone https://github.com/shraj19/flashdb.git || true
chown -R ec2-user:ec2-user /home/ec2-user/flashdb

# Note: Prometheus config will need to be updated with actual private IPs
# This is done manually after instances are created
EOF
)

    # Create EC2 instances
    log "Creating EC2 instances..."

    # Instance 1: FlashDB
    INSTANCE1_ID=$(aws ec2 run-instances \
        --region "$REGION" \
        --image-id "$AMI_ID" \
        --instance-type "$INSTANCE_TYPE" \
        --key-name "$KEY_NAME" \
        --subnet-id "$SUBNET_ID" \
        --security-group-ids "$SG_ID" \
        --user-data "$FLASHDB_USER_DATA" \
        --tag-specifications "ResourceType=instance,Tags=[{Key=$TAG_KEY,Value=$TAG_VALUE},{Key=Name,Value=$PROJECT_NAME-flashdb},{Key=Role,Value=flashdb}]" \
        --query 'Instances[0].InstanceId' \
        --output text)
    log "Created FlashDB instance: $INSTANCE1_ID"

    # Instance 2: Redis
    INSTANCE2_ID=$(aws ec2 run-instances \
        --region "$REGION" \
        --image-id "$AMI_ID" \
        --instance-type "$INSTANCE_TYPE" \
        --key-name "$KEY_NAME" \
        --subnet-id "$SUBNET_ID" \
        --security-group-ids "$SG_ID" \
        --user-data "$REDIS_USER_DATA" \
        --tag-specifications "ResourceType=instance,Tags=[{Key=$TAG_KEY,Value=$TAG_VALUE},{Key=Name,Value=$PROJECT_NAME-redis},{Key=Role,Value=redis}]" \
        --query 'Instances[0].InstanceId' \
        --output text)
    log "Created Redis instance: $INSTANCE2_ID"

    # Instance 3: Load Generator + Monitoring
    INSTANCE3_ID=$(aws ec2 run-instances \
        --region "$REGION" \
        --image-id "$AMI_ID" \
        --instance-type "$INSTANCE_TYPE" \
        --key-name "$KEY_NAME" \
        --subnet-id "$SUBNET_ID" \
        --security-group-ids "$SG_ID" \
        --user-data "$MONITORING_USER_DATA" \
        --tag-specifications "ResourceType=instance,Tags=[{Key=$TAG_KEY,Value=$TAG_VALUE},{Key=Name,Value=$PROJECT_NAME-loadgen},{Key=Role,Value=loadgen}]" \
        --query 'Instances[0].InstanceId' \
        --output text)
    log "Created Load Generator instance: $INSTANCE3_ID"

    # Create S3 bucket
    log "Creating S3 bucket..."
    if aws s3api head-bucket --bucket "$S3_BUCKET" 2>/dev/null; then
        log "S3 bucket already exists: $S3_BUCKET"
    else
        aws s3api create-bucket \
            --bucket "$S3_BUCKET" \
            --region "$REGION" \
            2>/dev/null || \
        aws s3api create-bucket \
            --bucket "$S3_BUCKET" \
            --region "$REGION" \
            --create-bucket-configuration LocationConstraint="$REGION"
        log "Created S3 bucket: $S3_BUCKET"
    fi

    # Create budget alarm
    log "Creating budget alarm ($30)..."
    aws budgets create-budget \
        --account-id "$(aws sts get-caller-identity --query Account --output text)" \
        --budget '{
            "BudgetName": "'"$PROJECT_NAME"'-budget",
            "BudgetLimit": {"Amount": "30", "Unit": "USD"},
            "BudgetType": "COST",
            "TimeUnit": "MONTHLY"
        }' \
        --notifications-with-subscribers '[{
            "Notification": {
                "NotificationType": "ACTUAL",
                "ComparisonOperator": "GREATER_THAN",
                "Threshold": 80,
                "ThresholdType": "PERCENTAGE"
            },
            "Subscribers": [{
                "SubscriptionType": "EMAIL",
                "Address": "'"$(aws sts get-caller-identity --query Arn --output text | sed 's/.*\///')"'@example.com"
            }]
        }]' 2>/dev/null || log "Budget might already exist or email needs configuration"

    # Save state
    save_state "$(cat <<EOF
{
    "vpc_id": "$VPC_ID",
    "igw_id": "$IGW_ID",
    "subnet_id": "$SUBNET_ID",
    "rtb_id": "$RTB_ID",
    "sg_id": "$SG_ID",
    "instance1_id": "$INSTANCE1_ID",
    "instance2_id": "$INSTANCE2_ID",
    "instance3_id": "$INSTANCE3_ID",
    "s3_bucket": "$S3_BUCKET",
    "key_name": "$KEY_NAME"
}
EOF
)"

    # Wait for instances to be running
    log "Waiting for instances to be running..."
    aws ec2 wait instance-running --region "$REGION" --instance-ids "$INSTANCE1_ID" "$INSTANCE2_ID" "$INSTANCE3_ID"

    log ""
    log "═══════════════════════════════════════════════════════════════════"
    log "INFRASTRUCTURE CREATED SUCCESSFULLY"
    log "═══════════════════════════════════════════════════════════════════"
    show_status
    log ""
    log "NEXT STEPS:"
    log "1. Wait 2-3 minutes for user-data scripts to complete"
    log "2. SSH into the loadgen instance and update prometheus.yml with private IPs"
    log "3. Run the benchmark: ./benchmark.sh --flashdb-host <FLASHDB_PRIVATE_IP> --redis-host <REDIS_PRIVATE_IP>"
    log ""
    log "To stop instances (save money): ./aws-provision.sh stop"
    log "To start instances again:       ./aws-provision.sh start"
}

# ─────────────────────────────────────────────────────────────────────────────
# Show Status
# ─────────────────────────────────────────────────────────────────────────────

show_status() {
    log "Instance Status:"
    log ""

    aws ec2 describe-instances \
        --region "$REGION" \
        --filters "Name=tag:$TAG_KEY,Values=$TAG_VALUE" "Name=instance-state-name,Values=pending,running,stopping,stopped" \
        --query 'Reservations[*].Instances[*].{Name:Tags[?Key==`Name`].Value|[0],ID:InstanceId,State:State.Name,Type:InstanceType,PrivateIP:PrivateIpAddress,PublicIP:PublicIpAddress}' \
        --output table

    log ""
    log "S3 Bucket: $(get_state_value s3_bucket)"
    log "SSH Key:   ~/.ssh/$(get_state_value key_name).pem"
}

# ─────────────────────────────────────────────────────────────────────────────
# Start/Stop Instances
# ─────────────────────────────────────────────────────────────────────────────

start_instances() {
    log "Starting instances..."
    INSTANCES=$(aws ec2 describe-instances \
        --region "$REGION" \
        --filters "Name=tag:$TAG_KEY,Values=$TAG_VALUE" "Name=instance-state-name,Values=stopped" \
        --query 'Reservations[*].Instances[*].InstanceId' \
        --output text)

    if [[ -z "$INSTANCES" ]]; then
        log "No stopped instances found."
        return
    fi

    aws ec2 start-instances --region "$REGION" --instance-ids $INSTANCES
    log "Starting: $INSTANCES"

    log "Waiting for instances to be running..."
    aws ec2 wait instance-running --region "$REGION" --instance-ids $INSTANCES
    log "Instances started."
    show_status
}

stop_instances() {
    log "Stopping instances..."
    INSTANCES=$(aws ec2 describe-instances \
        --region "$REGION" \
        --filters "Name=tag:$TAG_KEY,Values=$TAG_VALUE" "Name=instance-state-name,Values=running" \
        --query 'Reservations[*].Instances[*].InstanceId' \
        --output text)

    if [[ -z "$INSTANCES" ]]; then
        log "No running instances found."
        return
    fi

    aws ec2 stop-instances --region "$REGION" --instance-ids $INSTANCES
    log "Stopping: $INSTANCES"
    log "Instances will stop shortly. You will not be charged for stopped instances (only EBS storage)."
}

# ─────────────────────────────────────────────────────────────────────────────
# SSH Helper
# ─────────────────────────────────────────────────────────────────────────────

ssh_to_instance() {
    local instance_num="$1"
    local role=""

    case "$instance_num" in
        1) role="flashdb" ;;
        2) role="redis" ;;
        3) role="loadgen" ;;
        *) error "Invalid instance number. Use 1, 2, or 3." ;;
    esac

    local public_ip=$(aws ec2 describe-instances \
        --region "$REGION" \
        --filters "Name=tag:$TAG_KEY,Values=$TAG_VALUE" "Name=tag:Role,Values=$role" "Name=instance-state-name,Values=running" \
        --query 'Reservations[*].Instances[*].PublicIpAddress' \
        --output text)

    if [[ -z "$public_ip" || "$public_ip" == "None" ]]; then
        error "Instance $instance_num ($role) is not running or has no public IP."
    fi

    local key_file="$HOME/.ssh/${KEY_NAME}.pem"
    if [[ ! -f "$key_file" ]]; then
        error "SSH key not found: $key_file"
    fi

    log "Connecting to $role instance ($public_ip)..."
    ssh -i "$key_file" -o StrictHostKeyChecking=no ec2-user@"$public_ip"
}

# ─────────────────────────────────────────────────────────────────────────────
# Destroy Infrastructure
# ─────────────────────────────────────────────────────────────────────────────

destroy_infrastructure() {
    log "WARNING: This will terminate all instances and delete all resources!"
    read -p "Type 'yes' to confirm: " confirm
    if [[ "$confirm" != "yes" ]]; then
        log "Aborted."
        return
    fi

    # Terminate instances
    log "Terminating instances..."
    INSTANCES=$(aws ec2 describe-instances \
        --region "$REGION" \
        --filters "Name=tag:$TAG_KEY,Values=$TAG_VALUE" \
        --query 'Reservations[*].Instances[*].InstanceId' \
        --output text)

    if [[ -n "$INSTANCES" ]]; then
        aws ec2 terminate-instances --region "$REGION" --instance-ids $INSTANCES
        log "Waiting for termination..."
        aws ec2 wait instance-terminated --region "$REGION" --instance-ids $INSTANCES
    fi

    # Delete security group
    SG_ID=$(get_state_value sg_id)
    if [[ -n "$SG_ID" ]]; then
        log "Deleting security group..."
        aws ec2 delete-security-group --region "$REGION" --group-id "$SG_ID" 2>/dev/null || true
    fi

    # Delete subnet
    SUBNET_ID=$(get_state_value subnet_id)
    if [[ -n "$SUBNET_ID" ]]; then
        log "Deleting subnet..."
        aws ec2 delete-subnet --region "$REGION" --subnet-id "$SUBNET_ID" 2>/dev/null || true
    fi

    # Delete route table
    RTB_ID=$(get_state_value rtb_id)
    if [[ -n "$RTB_ID" ]]; then
        log "Deleting route table..."
        aws ec2 delete-route-table --region "$REGION" --route-table-id "$RTB_ID" 2>/dev/null || true
    fi

    # Detach and delete internet gateway
    IGW_ID=$(get_state_value igw_id)
    VPC_ID=$(get_state_value vpc_id)
    if [[ -n "$IGW_ID" && -n "$VPC_ID" ]]; then
        log "Deleting internet gateway..."
        aws ec2 detach-internet-gateway --region "$REGION" --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" 2>/dev/null || true
        aws ec2 delete-internet-gateway --region "$REGION" --internet-gateway-id "$IGW_ID" 2>/dev/null || true
    fi

    # Delete VPC
    if [[ -n "$VPC_ID" ]]; then
        log "Deleting VPC..."
        aws ec2 delete-vpc --region "$REGION" --vpc-id "$VPC_ID" 2>/dev/null || true
    fi

    # Note: S3 bucket and key pair are kept for potential future use
    log "Note: S3 bucket and SSH key pair were preserved. Delete manually if needed."

    rm -f "$STATE_FILE"
    log "Infrastructure destroyed."
}

# ─────────────────────────────────────────────────────────────────────────────
# Main
# ─────────────────────────────────────────────────────────────────────────────

case "${1:-help}" in
    create)   create_infrastructure ;;
    start)    start_instances ;;
    stop)     stop_instances ;;
    status)   show_status ;;
    destroy)  destroy_infrastructure ;;
    ssh)      ssh_to_instance "${2:-}" ;;
    help|*)
        echo "FlashDB Benchmark AWS Infrastructure"
        echo ""
        echo "Usage: $0 <command>"
        echo ""
        echo "Commands:"
        echo "  create    Create all infrastructure (VPC, EC2, S3, etc.)"
        echo "  start     Start stopped instances"
        echo "  stop      Stop running instances (saves money!)"
        echo "  status    Show instance status and IPs"
        echo "  destroy   Terminate all resources (CAREFUL!)"
        echo "  ssh <N>   SSH to instance N (1=FlashDB, 2=Redis, 3=LoadGen)"
        echo ""
        echo "Estimated cost: ~\$0.25/hr while running"
        echo ""
        ;;
esac
