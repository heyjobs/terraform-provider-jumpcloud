# Migration Guide: User Group Membership

This guide helps you migrate from the `jumpcloud_user_group_membership` resource pattern to the new `groups` field on the `jumpcloud_user` resource.

## Why Migrate?

The new `groups` field provides several benefits:

- **Simpler Configuration**: All user information, including group memberships, in one resource
- **Easier to Understand**: Looking at a user resource shows all their groups immediately
- **Less Resource Sprawl**: No need for multiple `jumpcloud_user_group_membership` resources per user
- **Atomic Operations**: User and all their group memberships are managed together
- **Consistent with Other Providers**: Similar to Okta and other identity provider patterns

## Migration Options

### Option 1: Manual Migration (Recommended for Production)

This is the safest approach for production environments as it gives you full control over the migration process.

#### Step 1: Review Current State

First, identify all users that use `jumpcloud_user_group_membership` resources:

```bash
# List all user group membership resources
terraform state list | grep jumpcloud_user_group_membership
```

#### Step 2: Backup State

**Always backup your state before making changes:**

```bash
terraform state pull > terraform.tfstate.backup
```

#### Step 3: Plan the Migration

For each user, collect their group memberships. For example, if you have:

```terraform
resource "jumpcloud_user" "john" {
  username = "john.doe"
  email    = "john.doe@acme.org"
}

resource "jumpcloud_user_group_membership" "john_to_devs" {
  userid  = jumpcloud_user.john.id
  groupid = jumpcloud_user_group.developers.id
}

resource "jumpcloud_user_group_membership" "john_to_admins" {
  userid  = jumpcloud_user.john.id
  groupid = jumpcloud_user_group.admins.id
}
```

#### Step 4: Update Configuration

Update your Terraform configuration to use the `groups` field:

```terraform
resource "jumpcloud_user" "john" {
  username = "john.doe"
  email    = "john.doe@acme.org"

  # Add all groups from membership resources
  groups = [
    jumpcloud_user_group.developers.id,
    jumpcloud_user_group.admins.id,
  ]
}

# Remove these resources:
# resource "jumpcloud_user_group_membership" "john_to_devs" { ... }
# resource "jumpcloud_user_group_membership" "john_to_admins" { ... }
```

#### Step 5: Remove Membership Resources from State

Before running `terraform apply`, remove the old membership resources from the state:

```bash
# Remove each membership resource
terraform state rm jumpcloud_user_group_membership.john_to_devs
terraform state rm jumpcloud_user_group_membership.john_to_admins
```

#### Step 6: Verify with Plan

Run `terraform plan` to verify the changes:

```bash
terraform plan
```

You should see that Terraform will update the user resource to set the `groups` field. **You should NOT see any operations to remove users from groups or create new memberships** (if you do, the groups in your configuration might not match the actual memberships).

#### Step 7: Apply Changes

Once the plan looks correct:

```bash
terraform apply
```

#### Step 8: Verify

Check that the user's groups are correctly set:

```bash
terraform state show jumpcloud_user.john
```

You should see the `groups` field populated with the group IDs.

### Option 2: State Migration Script

For larger migrations, you can use a script to automate the process:

```bash
#!/bin/bash
# migrate_user_groups.sh

USER_RESOURCE="jumpcloud_user.john"

# Get all membership resources for this user
MEMBERSHIPS=$(terraform state list | grep "jumpcloud_user_group_membership.*john")

# Remove each membership from state
for membership in $MEMBERSHIPS; do
  echo "Removing $membership from state..."
  terraform state rm "$membership"
done

echo "Migration complete. Update your Terraform configuration and run 'terraform plan' to verify."
```

### Option 3: Gradual Migration

Migrate users one at a time, keeping both patterns during the transition:

1. Choose a non-critical user to migrate first
2. Follow steps 1-8 from Option 1 for that user
3. Verify the user works correctly
4. Continue with more users
5. Keep unmigrated users using the old pattern

This approach allows you to:
- Test the new pattern in production with low risk
- Learn from any issues before migrating all users
- Maintain a working configuration at all times

## Common Scenarios

### Scenario 1: User with 3 Groups

**Before:**
```terraform
resource "jumpcloud_user" "alice" {
  username = "alice"
  email    = "alice@example.com"
}

resource "jumpcloud_user_group_membership" "alice_eng" {
  userid  = jumpcloud_user.alice.id
  groupid = jumpcloud_user_group.engineering.id
}

resource "jumpcloud_user_group_membership" "alice_dev" {
  userid  = jumpcloud_user.alice.id
  groupid = jumpcloud_user_group.developers.id
}

resource "jumpcloud_user_group_membership" "alice_fe" {
  userid  = jumpcloud_user.alice.id
  groupid = jumpcloud_user_group.frontend.id
}
```

**After:**
```terraform
resource "jumpcloud_user" "alice" {
  username = "alice"
  email    = "alice@example.com"
  groups   = [
    jumpcloud_user_group.engineering.id,
    jumpcloud_user_group.developers.id,
    jumpcloud_user_group.frontend.id,
  ]
}
```

**Migration Commands:**
```bash
terraform state rm jumpcloud_user_group_membership.alice_eng
terraform state rm jumpcloud_user_group_membership.alice_dev
terraform state rm jumpcloud_user_group_membership.alice_fe
terraform plan  # Verify
terraform apply
```

### Scenario 2: User with No Groups

**Before:**
```terraform
resource "jumpcloud_user" "bob" {
  username = "bob"
  email    = "bob@example.com"
}
```

**After:**
```terraform
resource "jumpcloud_user" "bob" {
  username = "bob"
  email    = "bob@example.com"
  # No groups field needed - user has no groups
}
```

**No migration needed** - user already has no groups.

### Scenario 3: Multiple Users in Same Groups

**Before:**
```terraform
resource "jumpcloud_user" "user1" {
  username = "user1"
  email    = "user1@example.com"
}

resource "jumpcloud_user" "user2" {
  username = "user2"
  email    = "user2@example.com"
}

resource "jumpcloud_user_group_membership" "user1_dev" {
  userid  = jumpcloud_user.user1.id
  groupid = jumpcloud_user_group.developers.id
}

resource "jumpcloud_user_group_membership" "user2_dev" {
  userid  = jumpcloud_user.user2.id
  groupid = jumpcloud_user_group.developers.id
}
```

**After:**
```terraform
resource "jumpcloud_user" "user1" {
  username = "user1"
  email    = "user1@example.com"
  groups   = [jumpcloud_user_group.developers.id]
}

resource "jumpcloud_user" "user2" {
  username = "user2"
  email    = "user2@example.com"
  groups   = [jumpcloud_user_group.developers.id]
}
```

### Scenario 4: Module-Based Configuration

If you're using modules, you have two options:

**Option A: Update Module to Use Groups Field**

```terraform
# modules/user/main.tf
variable "username" { type = string }
variable "email" { type = string }
variable "group_ids" {
  type    = list(string)
  default = []
}

resource "jumpcloud_user" "this" {
  username = var.username
  email    = var.email
  groups   = var.group_ids
}
```

**Option B: Keep Module Using Membership Resources**

If you have many consumers of your module, you might want to keep the old pattern to avoid breaking changes:

```terraform
# Keep existing module-based membership approach
# Add new module for users with groups field
```

## Rollback Procedure

If you need to rollback after migration:

### Step 1: Restore State Backup

```bash
cp terraform.tfstate.backup terraform.tfstate
```

### Step 2: Restore Configuration

Revert your Terraform configuration files to use `jumpcloud_user_group_membership` resources.

### Step 3: Verify

```bash
terraform plan
```

Should show no changes.

## Handling Edge Cases

### Mixed Approach During Migration

During migration, you might have some users using the new pattern and some using the old:

```terraform
# Old pattern - not yet migrated
resource "jumpcloud_user" "bob" {
  username = "bob"
  email    = "bob@example.com"
}

resource "jumpcloud_user_group_membership" "bob_dev" {
  userid  = jumpcloud_user.bob.id
  groupid = jumpcloud_user_group.developers.id
}

# New pattern - already migrated
resource "jumpcloud_user" "alice" {
  username = "alice"
  email    = "alice@example.com"
  groups   = [
    jumpcloud_user_group.developers.id,
    jumpcloud_user_group.admins.id,
  ]
}
```

This is perfectly fine during migration! Just ensure you don't use both patterns for the same user.

### User Already Has Groups Not in Terraform

If a user has group memberships created outside of Terraform:

1. **Before migration**, the `jumpcloud_user_group_membership` resources don't manage those groups, so they remain untouched
2. **After migration**, the `groups` field will show all groups in state (via Read operation)
3. **On first apply**, Terraform will remove the user from groups not in your configuration

To avoid removing unmanaged groups:

```terraform
# Import all current groups into state first
resource "jumpcloud_user" "john" {
  username = "john.doe"
  email    = "john.doe@acme.org"

  groups = [
    jumpcloud_user_group.managed_group_1.id,
    jumpcloud_user_group.managed_group_2.id,
    # Add any groups that exist but aren't in Terraform
    "group_id_not_managed_by_terraform",
  ]
}
```

Or manage them separately using `jumpcloud_user_group_membership`.

## Verification Checklist

After migration, verify:

- [ ] User can still log in
- [ ] User has access to expected systems/applications
- [ ] `terraform plan` shows no unexpected changes
- [ ] State file shows `groups` field populated
- [ ] Old membership resources removed from state
- [ ] Old membership resources removed from configuration
- [ ] Backup of old state file is saved

## Troubleshooting

### Issue: Terraform wants to remove and re-add groups

**Cause:** Group IDs in configuration don't match actual memberships

**Solution:** Check the group IDs are correct. Use `terraform state show jumpcloud_user.username` to see current groups.

### Issue: "Conflict" errors during apply

**Cause:** User still has `jumpcloud_user_group_membership` resources in state

**Solution:** Ensure all membership resources are removed from state before applying:
```bash
terraform state list | grep jumpcloud_user_group_membership
```

### Issue: Groups field shows empty after migration

**Cause:** Groups not saved during migration, or Read operation failing

**Solution:**
1. Check Terraform logs: `TF_LOG=DEBUG terraform apply`
2. Verify user exists in JumpCloud
3. Verify group IDs are correct

### Issue: User removed from groups during apply

**Cause:** Groups list in configuration is incomplete

**Solution:** Ensure all desired groups are in the `groups` list in your configuration.

## Best Practices

1. **Migrate in Non-Production First**: Test the migration process in a dev/staging environment
2. **Migrate in Batches**: Don't migrate all users at once
3. **Document Your Process**: Keep notes on what works for your specific setup
4. **Communicate with Team**: Let your team know about the migration
5. **Monitor After Migration**: Check logs and user access after migration
6. **Keep Backups**: Always maintain state backups during migration

## Getting Help

If you encounter issues during migration:

1. Check the logs with `TF_LOG=DEBUG terraform apply`
2. Review the [User Resource Documentation](docs/resources/user.md)
3. Check the JumpCloud admin console to verify actual state
4. Restore from backup if needed

## Timeline Recommendation

For organizations with many users:

- **Week 1**: Test migration with 1-2 non-critical users
- **Week 2**: Migrate 10-20% of users
- **Week 3**: Migrate 50% of users
- **Week 4**: Migrate remaining users
- **Week 5**: Remove old membership resources and clean up

For smaller organizations:

- Migrate all at once, or
- Migrate users as you touch their configuration naturally
