#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <libusb-1.0/libusb.h>

#define TARGET_VID 0x04e8
#define CMD_DATA "DVIF"
#define CMD_LEN 4
#define TIMEOUT_MS 5000
#define CHUNK_SIZE 16384
#define MAX_DATA_SIZE 65536 // Maksimum 64KB veri bekliyoruz

// Bulunan cihaz bilgileri
struct DeviceConfig {
    int pid;
    int interface_num;
    int ep_in;
    int ep_out;
};

// Cihazı ve uygun endpointleri otomatik bulur
int find_device_and_endpoints(libusb_context *ctx, libusb_device_handle **h, struct DeviceConfig *config) {
    libusb_device **devs;
    ssize_t cnt = libusb_get_device_list(ctx, &devs);
    if (cnt < 0) return -1;

    libusb_device *dev;
    int i = 0;
    int found = 0;

    while ((dev = devs[i++]) != NULL) {
        struct libusb_device_descriptor desc;
        int r = libusb_get_device_descriptor(dev, &desc);
        if (r < 0) continue;

        if (desc.idVendor == TARGET_VID) {
            struct libusb_config_descriptor *config_desc;
            libusb_get_config_descriptor(dev, 0, &config_desc);
            
            for (int j = 0; j < config_desc->bNumInterfaces; j++) {
                const struct libusb_interface *inter = &config_desc->interface[j];
                const struct libusb_interface_descriptor *inter_desc = &inter->altsetting[0];

                int found_in = 0;
                int found_out = 0;
                int temp_ep_in = 0;
                int temp_ep_out = 0;

                for (int k = 0; k < inter_desc->bNumEndpoints; k++) {
                    const struct libusb_endpoint_descriptor *ep_desc = &inter_desc->endpoint[k];
                    if ((ep_desc->bmAttributes & LIBUSB_TRANSFER_TYPE_MASK) == LIBUSB_TRANSFER_TYPE_BULK) {
                        if ((ep_desc->bEndpointAddress & LIBUSB_ENDPOINT_DIR_MASK) == LIBUSB_ENDPOINT_IN) {
                            if (!found_in) { temp_ep_in = ep_desc->bEndpointAddress; found_in = 1; }
                        } else {
                            if (!found_out) { temp_ep_out = ep_desc->bEndpointAddress; found_out = 1; }
                        }
                    }
                }

                if (found_in && found_out) {
                    r = libusb_open(dev, h);
                    if (r == 0) {
                        config->pid = desc.idProduct;
                        config->interface_num = inter_desc->bInterfaceNumber;
                        config->ep_in = temp_ep_in;
                        config->ep_out = temp_ep_out;
                        found = 1;
                    }
                    libusb_free_config_descriptor(config_desc);
                    goto cleanup;
                }
            }
            libusb_free_config_descriptor(config_desc);
        }
    }

cleanup:
    libusb_free_device_list(devs, 1);
    return found ? 0 : -1;
}

int main() {
    libusb_context *ctx = NULL;
    libusb_device_handle *h = NULL;
    struct DeviceConfig config = {0};
    int ret;

    if (libusb_init(&ctx) != 0) return 1;

    if (find_device_and_endpoints(ctx, &h, &config) != 0) {
        libusb_exit(ctx);
        return 1;
    }

    if (libusb_kernel_driver_active(h, config.interface_num) == 1) {
        libusb_detach_kernel_driver(h, config.interface_num);
    }

    if (libusb_claim_interface(h, config.interface_num) != 0) {
        libusb_close(h);
        libusb_exit(ctx);
        return 1;
    }

    int transferred = 0;
    ret = libusb_bulk_transfer(h, (unsigned char)config.ep_out, (unsigned char*)CMD_DATA, CMD_LEN, &transferred, TIMEOUT_MS);
    if (ret != 0) goto cleanup;

    unsigned char *chunk_buf = malloc(CHUNK_SIZE);
    char *total_data = malloc(MAX_DATA_SIZE);
    int total_len = 0;
    int start_found = 0;

    if (!chunk_buf || !total_data) goto cleanup;

    memset(total_data, 0, MAX_DATA_SIZE);

    while (1) {
        ret = libusb_bulk_transfer(h, (unsigned char)config.ep_in, chunk_buf, CHUNK_SIZE, &transferred, TIMEOUT_MS);
        
        if (transferred > 0) {
            // Tampon taşmasını önle
            if (total_len + transferred >= MAX_DATA_SIZE) {
                break; 
            }

            // Gelen veriyi ana belleğe ekle
            memcpy(total_data + total_len, chunk_buf, transferred);
            total_len += transferred;
            total_data[total_len] = '\0'; // String işlemleri için null terminator

            // Başlangıç "@#" kontrolü
            char *start_ptr = strstr(total_data, "@#");
            if (!start_found && start_ptr) {
                start_found = 1;
                fprintf(stderr, "Receiving data...\n");
            }

            // Bitiş kontrolü: Başlangıç bulunduktan SONRA gelen bir "@#" daha var mı?
            // Veya "#@" ile bitiyor olabilir, her ihtimale karşı "#@" da kontrol edelim.
            if (start_found) {
                char *search_start = start_ptr + 2; // İlk @# sonrasından aramaya başla
                
                // İkinci "@#" var mı?
                char *end_ptr_1 = strstr(search_start, "@#");
                // Yada "#@" var mı? (Samsung standardı genelde budur)
                char *end_ptr_2 = strstr(search_start, "#@");

                if (end_ptr_1 || end_ptr_2) {
                    // Veri tamamlandı!
                    // Ekrana bas ve çık
                    fwrite(total_data, 1, total_len, stdout);
                    fflush(stdout);
                    break;
                }
            }
        }

        if (ret != 0) {
            // Hata veya timeout durumunda, elimizdekini basıp çıkalım
            if (total_len > 0) {
                fwrite(total_data, 1, total_len, stdout);
                fflush(stdout);
            }
            break;
        }
    }

    free(chunk_buf);
    free(total_data);

cleanup:
    libusb_release_interface(h, config.interface_num);
    libusb_close(h);
    libusb_exit(ctx);
    return 0;
}
