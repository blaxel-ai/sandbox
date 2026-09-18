// SPDX-License-Identifier: GPL-2.0
/*
 * virtio_ring_resync: recover a split virtqueue the guest driver marked broken
 * ("id N is not a head!") on a device that cannot be reset.
 *
 * After a snapshot/restore race the device's used index can lag behind what
 * the driver already consumed. The driver then reads a stale used-ring slot,
 * calls BAD_RING() and sets vq->broken, which is never cleared again outside a
 * device reset - and Firecracker turns a reset of an activated net device into
 * a permanent FAILED status. The device itself keeps running on the very same
 * rings though, so the only thing that needs fixing is the driver's view:
 *
 *   1. last_used_idx := used->idx    (skip the stale slots)
 *   2. used_event    := used->idx    (so the device notifies us again)
 *   3. broken        := false
 *   4. vring_interrupt()             (drain whatever is pending now)
 *
 * The buffers sitting in the skipped slots are not returned to the driver:
 * they stay allocated until the device is torn down. On the rx queue that is
 * a few sk_buffs the driver simply refills; on the tx queue a few sk_buffs
 * the stack will never get completions for. Both are the price of not
 * restarting the VM.
 *
 * The module works on the virtio device behind a net_device (netdev=eth0). It
 * only touches queues whose layout it can cross-check against exported
 * accessors, so a kernel with a different struct vring_virtqueue refuses to
 * do anything instead of corrupting memory. It does all its work in init and
 * is meant to be unloaded straight away.
 */

#include <linux/module.h>
#include <linux/netdevice.h>
#include <linux/virtio.h>
#include <linux/virtio_config.h>
#include <linux/virtio_ring.h>
#include <linux/interrupt.h>

/*
 * Private layout of drivers/virtio/virtio_ring.c (v6.12, !DEBUG). Only the
 * fields up to and including `split` are used; the trailing ones are omitted.
 */
struct vring_virtqueue_split {
	struct vring vring;
	u16 avail_flags_shadow;
	u16 avail_idx_shadow;
	void *desc_state;
	void *desc_extra;
	dma_addr_t queue_dma_addr;
	size_t queue_size_in_bytes;
	u32 vring_align;
	bool may_reduce_num;
};

struct vring_virtqueue {
	struct virtqueue vq;
	bool packed_ring;
	bool use_dma_api;
	bool weak_barriers;
	bool broken;
	bool indirect;
	bool event;
	bool premapped;
	bool do_unmap;
	unsigned int free_head;
	unsigned int num_added;
	u16 last_used_idx;
	bool event_triggered;
	union {
		struct vring_virtqueue_split split;
	};
};

static char *netdev = "eth0";
module_param(netdev, charp, 0);
MODULE_PARM_DESC(netdev, "network interface whose virtio queues to resync");

static char *queue = "";
module_param(queue, charp, 0);
MODULE_PARM_DESC(queue, "only resync this queue (e.g. input.0); default: every broken queue");

static bool force;
module_param(force, bool, 0);
MODULE_PARM_DESC(force, "resync the selected queue(s) even if not marked broken");

static bool break_queues;
module_param(break_queues, bool, 0);
MODULE_PARM_DESC(break_queues, "testing only: mark the selected queue(s) broken instead of resyncing them");

static int resynced;
module_param(resynced, int, 0444);

static bool layout_matches(struct virtqueue *_vq, struct vring_virtqueue *vq)
{
	const struct vring *ring;

	if (vq->packed_ring)
		return false;
	ring = virtqueue_get_vring(_vq);
	if (ring != &vq->split.vring)
		return false;
	if (vq->split.vring.num != virtqueue_get_vring_size(_vq))
		return false;
	if (vq->split.vring.num == 0 || (vq->split.vring.num & (vq->split.vring.num - 1)))
		return false;
	return true;
}

static void resync_one(struct virtio_device *vdev, struct virtqueue *_vq,
		       struct vring_virtqueue *vq)
{
	u16 used_idx = virtio16_to_cpu(vdev, vq->split.vring.used->idx);

	dev_warn(&vdev->dev,
		 "%s: resync (broken=%d last_used_idx=%u used->idx=%u num_free=%u/%u)\n",
		 _vq->name, vq->broken, vq->last_used_idx, used_idx,
		 _vq->num_free, vq->split.vring.num);

	vq->last_used_idx = used_idx;
	if (vq->event)
		vring_used_event(&vq->split.vring) = cpu_to_virtio16(vdev, used_idx);
	else
		vq->split.vring.avail->flags = cpu_to_virtio16(vdev, vq->split.avail_flags_shadow);
	/* publish the index before letting anybody use the queue again */
	smp_wmb();
	WRITE_ONCE(vq->broken, false);
	resynced++;
}

static int __init virtio_ring_resync_init(void)
{
	struct net_device *ndev;
	struct virtio_device *vdev;
	struct virtqueue *_vq;
	int ret = 0;

	ndev = dev_get_by_name(&init_net, netdev);
	if (!ndev)
		return -ENODEV;
	if (!ndev->dev.parent || !ndev->dev.parent->bus ||
	    strcmp(ndev->dev.parent->bus->name, "virtio") != 0) {
		dev_put(ndev);
		return -EOPNOTSUPP;
	}
	vdev = dev_to_virtio(ndev->dev.parent);

	spin_lock(&vdev->vqs_list_lock);
	list_for_each_entry(_vq, &vdev->vqs, list) {
		struct vring_virtqueue *vq = container_of(_vq, struct vring_virtqueue, vq);

		if (!layout_matches(_vq, vq)) {
			dev_err(&vdev->dev, "%s: unexpected virtqueue layout, refusing\n", _vq->name);
			ret = -EPROTO;
			break;
		}
	}
	if (!ret) {
		list_for_each_entry(_vq, &vdev->vqs, list) {
			struct vring_virtqueue *vq = container_of(_vq, struct vring_virtqueue, vq);

			if (*queue && strcmp(_vq->name, queue) != 0)
				continue;
			if (break_queues) {
				dev_warn(&vdev->dev, "%s: marking broken for testing\n", _vq->name);
				__virtqueue_break(_vq);
				resynced++;
				continue;
			}
			if (!READ_ONCE(vq->broken) && !force)
				continue;
			resync_one(vdev, _vq, vq);
		}
	}
	spin_unlock(&vdev->vqs_list_lock);

	if (!ret) {
		if (!break_queues)
			list_for_each_entry(_vq, &vdev->vqs, list)
				vring_interrupt(0, _vq);
		if (!resynced)
			ret = -ENOENT;
	}

	dev_put(ndev);
	return ret;
}

static void __exit virtio_ring_resync_exit(void)
{
}

module_init(virtio_ring_resync_init);
module_exit(virtio_ring_resync_exit);

MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Resync a broken split virtqueue with a device that cannot be reset");
